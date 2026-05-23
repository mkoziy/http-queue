package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	badger "github.com/dgraph-io/badger/v4"

	"github.com/mkoziy/http-queue/config"
	"github.com/mkoziy/http-queue/db"
)

// Sweeper periodically performs expiry and reconciliation sweeps.
type Sweeper struct {
	db  *badger.DB
	cfg *config.Config
}

// NewSweeper creates a new Sweeper.
func NewSweeper(database *badger.DB, cfg *config.Config) *Sweeper {
	return &Sweeper{db: database, cfg: cfg}
}

// Start begins the sweeper loop. It runs until the context is cancelled.
// The immediate startup pass performs only startup-safe maintenance:
// expired-reservation re-queuing and reconciliation, but NOT worker expiry.
// Worker expiry is deferred to the first periodic tick so that durable
// workers have a chance to reconnect after a restart before being removed.
func (s *Sweeper) Start(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.SweepInterval)
	defer ticker.Stop()

	// Run a startup-safe sweep immediately — no worker expiry.
	s.startupSweep()

	for {
		select {
		case <-ticker.C:
			s.sweep()
		case <-ctx.Done():
			return
		}
	}
}

// startupSweep performs only the maintenance operations that are safe to
// run immediately on startup. Worker expiry is excluded so that durable
// workers whose LastSeen is stale are not removed before they can reconnect.
func (s *Sweeper) startupSweep() {
	s.expireReservations()
	s.reconcile()
}

func (s *Sweeper) sweep() {
	s.expireWorkers()
	s.expireReservations()
	s.reconcile()
}

// expireWorkers removes workers whose last-seen exceeds WorkerExpiry.
func (s *Sweeper) expireWorkers() {
	expiryThreshold := time.Now().UTC().Add(-s.cfg.WorkerExpiry)

	var expiredIDs []string

	// Collect expired worker IDs.
	err := s.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()

		prefix := []byte(db.WorkerPrefix)
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			data, err := item.ValueCopy(nil)
			if err != nil {
				continue
			}

			var w Worker
			if err := json.Unmarshal(data, &w); err != nil {
				continue
			}

			if w.LastSeen.Before(expiryThreshold) {
				// Check in-memory last-seen cache first — may be fresher than DB
				// due to TouchWorker debounce.
				if ls, ok := workerLastSeen.Load(w.ID); ok {
					if lastSeen, ok := ls.(time.Time); ok && !lastSeen.Before(expiryThreshold) {
						continue
					}
				}
				expiredIDs = append(expiredIDs, w.ID)
			}
		}
		return nil
	})
	if err != nil {
		log.Printf("sweep: view workers: %v", err)
		return
	}

	// Deregister each expired worker.
	for _, id := range expiredIDs {
		// Re-check in-memory cache: worker may have become active after
		// the View snapshot was taken (e.g., sent a claim/ack/nack request).
		if ls, ok := workerLastSeen.Load(id); ok {
			if lastSeen, ok := ls.(time.Time); ok && !lastSeen.Before(expiryThreshold) {
				continue
			}
		}
		if err := DeregisterWorker(s.db, id); err != nil {
			log.Printf("sweep: deregister expired worker %s: %v", id, err)
		} else {
			log.Printf("sweep: expired worker %s removed", id)
		}
	}
}

// expireReservations re-queues reservations that have exceeded their visibility timeout.
// Collection happens in a read transaction; mutations happen in bounded write batches
// to prevent unbounded key mutations in a single BadgerDB transaction.
func (s *Sweeper) expireReservations() {
	now := time.Now().UTC().Unix()

	// Step 1: Collect expired reservation refs in a read transaction.
	var refs []reservedRef

	err := s.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()

		prefix := []byte("queue:")
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			key := string(item.Key())

			// Check if it's a reserved index key.
			if !strings.Contains(key, ":reserved:") {
				continue
			}

			// Parse the expiry value.
			val, err := item.ValueCopy(nil)
			if err != nil {
				continue
			}

			expiryUnix, err := strconv.ParseInt(string(val), 10, 64)
			if err != nil {
				continue
			}

			if now < expiryUnix {
				// Not yet expired.
				continue
			}

			// Extract queue name and ULID.
			parts := strings.SplitN(key, ":", 4)
			if len(parts) < 4 {
				continue
			}

			refs = append(refs, reservedRef{
				queue: parts[1],
				ulid:  parts[3],
			})
		}
		return nil
	})
	if err != nil {
		log.Printf("sweep: view expired reservations: %v", err)
		return
	}

	// Step 2: Process refs in bounded write batches.
	for batchIdx, batch := range batchSlice(refs, maintenanceBatchSize) {
		if err := s.expireReservationBatch(now, batch); err != nil {
			log.Printf("sweep: expire reservations batch %d: %v", batchIdx, err)
		}
	}
}

// expireReservationBatch re-queues or dead-letters a batch of expired reservation
// refs in a single write transaction. Each ref is re-checked: the reserved index
// must still exist and still be expired, and the job record is loaded fresh.
func (s *Sweeper) expireReservationBatch(now int64, refs []reservedRef) error {
	if len(refs) == 0 {
		return nil
	}

	err := s.db.Update(func(txn *badger.Txn) error {
		for _, ref := range refs {
			reservedKey := db.ReservedIndexKey(ref.queue, ref.ulid)

			// Re-check the reserved index still exists and is still expired.
			reservedItem, err := txn.Get(reservedKey)
			if err != nil {
				// Already removed between collection and batch; skip.
				continue
			}

			val, err := reservedItem.ValueCopy(nil)
			if err != nil {
				continue
			}

			expiryUnix, err := strconv.ParseInt(string(val), 10, 64)
			if err != nil {
				continue
			}

			if now < expiryUnix {
				// No longer expired (was extended between collection and batch); skip.
				continue
			}

			// Load job record.
			jobItem, err := txn.Get(db.JobKey(ref.ulid))
			if err != nil {
				// Orphaned index; delete it.
				_ = txn.Delete(reservedKey)
				continue
			}

			jobData, err := jobItem.ValueCopy(nil)
			if err != nil {
				continue
			}

			var job Job
			if err := json.Unmarshal(jobData, &job); err != nil {
				continue
			}

			// Check MAX_ATTEMPTS.
			if job.Attempts >= s.cfg.MaxAttempts {
				// Move to dead-letter.
				job.Status = StatusDead
				job.WorkerID = ""
				updatedData, err := json.Marshal(job)
				if err != nil {
					return fmt.Errorf("marshal dead job %s: %w", ref.ulid, err)
				}
				if err := txn.Delete(reservedKey); err != nil {
					return fmt.Errorf("delete reserved index for dead job %s: %w", ref.ulid, err)
				}
				if err := txn.Set(db.DeadIndexKey(ref.queue, ref.ulid), nil); err != nil {
					return fmt.Errorf("set dead index for job %s: %w", ref.ulid, err)
				}
				if err := txn.Set(db.JobKey(ref.ulid), updatedData); err != nil {
					return fmt.Errorf("update dead job %s: %w", ref.ulid, err)
				}
				log.Printf("sweep: job %s moved to dead-letter (max attempts)", ref.ulid)
			} else {
				// Re-queue.
				job.Status = StatusPending
				job.WorkerID = ""
				updatedData, err := json.Marshal(job)
				if err != nil {
					return fmt.Errorf("marshal re-queued job %s: %w", ref.ulid, err)
				}
				if err := txn.Delete(reservedKey); err != nil {
					return fmt.Errorf("delete expired reserved index for job %s: %w", ref.ulid, err)
				}
				if err := txn.Set(db.PendingIndexKey(ref.queue, ref.ulid), nil); err != nil {
					return fmt.Errorf("set pending index for re-queued job %s: %w", ref.ulid, err)
				}
				if err := txn.Set(db.JobKey(ref.ulid), updatedData); err != nil {
					return fmt.Errorf("update re-queued job %s: %w", ref.ulid, err)
				}
				log.Printf("sweep: job %s re-queued (visibility timeout expired)", ref.ulid)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("process expired reservation batch: %w", err)
	}
	return nil
}

// reconcileJobRef identifies a job-record inconsistency found during reconciliation.
type reconcileJobRef struct {
	jobID              string
	queue              string
	isOrphanedReserved bool
}

// phantomIndexRef identifies a phantom index key to delete during reconciliation.
type phantomIndexRef struct {
	jobID string
	queue string
	kind  string // "pending" or "reserved"
}

// reconcile checks for orphaned records and fixes inconsistencies.
// Collection happens in read transactions; mutations happen in bounded write batches
// to prevent unbounded key mutations in a single BadgerDB transaction.
func (s *Sweeper) reconcile() {
	s.reconcileJobRecords()
	s.reconcilePhantomIndexes()
}

// reconcileJobRecords scans job records for inconsistencies and fixes them in batches.
func (s *Sweeper) reconcileJobRecords() {
	var ops []reconcileJobRef

	err := s.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()

		prefix := []byte("job:")
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			data, err := item.ValueCopy(nil)
			if err != nil {
				continue
			}

			var job Job
			if err := json.Unmarshal(data, &job); err != nil {
				continue
			}

			switch job.Status {
			case StatusReserved:
				// Check if reserved index is missing (orphaned reserved job).
				_, err := txn.Get(db.ReservedIndexKey(job.Queue, job.ID))
				if err != nil {
					ops = append(ops, reconcileJobRef{
						jobID:              job.ID,
						queue:              job.Queue,
						isOrphanedReserved: true,
					})
				}
			case StatusPending:
				// Check if pending index is missing.
				_, err := txn.Get(db.PendingIndexKey(job.Queue, job.ID))
				if err != nil {
					ops = append(ops, reconcileJobRef{
						jobID: job.ID,
						queue: job.Queue,
					})
				}
			}
		}
		return nil
	})
	if err != nil {
		log.Printf("sweep: reconcile collect job records: %v", err)
		return
	}

	for batchIdx, batch := range batchSlice(ops, maintenanceBatchSize) {
		if err := s.applyReconcileJobBatch(batch); err != nil {
			log.Printf("sweep: reconcile job records batch %d: %v", batchIdx, err)
		}
	}
}

// applyReconcileJobBatch processes a batch of job-record fix-ups in a single write transaction.
// Each ref is re-checked to confirm the inconsistency still exists.
func (s *Sweeper) applyReconcileJobBatch(ops []reconcileJobRef) error {
	if len(ops) == 0 {
		return nil
	}

	err := s.db.Update(func(txn *badger.Txn) error {
		for _, op := range ops {
			if op.isOrphanedReserved {
				// Re-check: job still reserved and reserved index still missing.
				jobItem, err := txn.Get(db.JobKey(op.jobID))
				if err != nil {
					continue
				}
				data, err := jobItem.ValueCopy(nil)
				if err != nil {
					continue
				}
				var job Job
				if err := json.Unmarshal(data, &job); err != nil {
					continue
				}
				if job.Status != StatusReserved {
					continue
				}
				_, err = txn.Get(db.ReservedIndexKey(job.Queue, job.ID))
				if err == nil {
					// Reserved index now exists; not orphaned anymore.
					continue
				}

				// Fix: re-queue as pending.
				job.Status = StatusPending
				job.WorkerID = ""
				updatedData, err := json.Marshal(job)
				if err != nil {
					return fmt.Errorf("marshal reconciled job %s: %w", op.jobID, err)
				}
				if err := txn.Set(db.JobKey(op.jobID), updatedData); err != nil {
					return fmt.Errorf("set reconciled job %s: %w", op.jobID, err)
				}
				if err := txn.Set(db.PendingIndexKey(op.queue, op.jobID), nil); err != nil {
					return fmt.Errorf("set reconciled pending index for job %s: %w", op.jobID, err)
				}
				log.Printf("sweep: reconciled orphaned reserved job %s -> pending", op.jobID)
			} else {
				// Re-check: job still exists and is still pending.
				jobItem, err := txn.Get(db.JobKey(op.jobID))
				if err != nil {
					continue
				}
				data, err := jobItem.ValueCopy(nil)
				if err != nil {
					continue
				}
				var job Job
				if err := json.Unmarshal(data, &job); err != nil {
					continue
				}
				if job.Status != StatusPending {
					continue
				}

				// Re-check: pending index still missing.
				_, err = txn.Get(db.PendingIndexKey(op.queue, op.jobID))
				if err == nil {
					continue
				}

				// Re-create missing pending index.
				if err := txn.Set(db.PendingIndexKey(op.queue, op.jobID), nil); err != nil {
					return fmt.Errorf("set missing pending index for job %s: %w", op.jobID, err)
				}
				log.Printf("sweep: reconciled missing pending index for job %s", op.jobID)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("apply reconcile job batch: %w", err)
	}
	return nil
}

// reconcilePhantomIndexes scans index keys and removes phantom entries (no matching job record).
// Covers both pending and reserved indexes.
func (s *Sweeper) reconcilePhantomIndexes() {
	var phantoms []phantomIndexRef

	err := s.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()

		prefix := []byte("queue:")
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			key := string(item.Key())

			// Check for pending or reserved indexes.
			var kind string
			if strings.Contains(key, ":pending:") {
				kind = "pending"
			} else if strings.Contains(key, ":reserved:") {
				kind = "reserved"
			} else {
				continue
			}

			parts := strings.SplitN(key, ":", 4)
			if len(parts) < 4 {
				continue
			}
			ulidStr := parts[3]

			// Verify job record exists.
			_, err := txn.Get(db.JobKey(ulidStr))
			if err != nil {
				phantoms = append(phantoms, phantomIndexRef{
					jobID: ulidStr,
					queue: parts[1],
					kind:  kind,
				})
			}
		}
		return nil
	})
	if err != nil {
		log.Printf("sweep: collect phantom indexes: %v", err)
		return
	}

	for batchIdx, batch := range batchSlice(phantoms, maintenanceBatchSize) {
		if err := s.applyPhantomIndexBatch(batch); err != nil {
			log.Printf("sweep: remove phantom indexes batch %d: %v", batchIdx, err)
		}
	}
}

// applyPhantomIndexBatch removes a batch of phantom index keys in a single write transaction.
// Each entry is re-checked: the job record must still be absent.
func (s *Sweeper) applyPhantomIndexBatch(phantoms []phantomIndexRef) error {
	if len(phantoms) == 0 {
		return nil
	}

	err := s.db.Update(func(txn *badger.Txn) error {
		for _, ph := range phantoms {
			// Re-check job record is still absent.
			_, err := txn.Get(db.JobKey(ph.jobID))
			if err == nil {
				// Job record now exists; not a phantom.
				continue
			}

			var key []byte
			switch ph.kind {
			case "pending":
				key = db.PendingIndexKey(ph.queue, ph.jobID)
			case "reserved":
				key = db.ReservedIndexKey(ph.queue, ph.jobID)
			default:
				continue
			}

			if err := txn.Delete(key); err != nil {
				return fmt.Errorf("delete phantom %s index %s: %w", ph.kind, string(key), err)
			}
			log.Printf("sweep: removed phantom %s index for %s/%s", ph.kind, ph.queue, ph.jobID)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("apply phantom index batch: %w", err)
	}
	return nil
}
