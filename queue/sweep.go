package queue

import (
	"context"
	"encoding/json"
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
func (s *Sweeper) Start(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.SweepInterval)
	defer ticker.Stop()

	// Run one sweep immediately on start.
	s.sweep()

	for {
		select {
		case <-ticker.C:
			s.sweep()
		case <-ctx.Done():
			return
		}
	}
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
func (s *Sweeper) expireReservations() {
	now := time.Now().UTC().Unix()

	err := s.db.Update(func(txn *badger.Txn) error {
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
			queueName := parts[1]
			ulidStr := parts[3]

			// Load job record.
			jobItem, err := txn.Get(db.JobKey(ulidStr))
			if err != nil {
				// Orphaned index; delete it.
				_ = txn.Delete(item.Key())
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
					log.Printf("sweep: marshal dead job %s: %v", ulidStr, err)
					continue
				}
				if err := txn.Delete(item.Key()); err != nil {
					log.Printf("sweep: delete reserved index for dead job %s: %v", ulidStr, err)
					continue
				}
				if err := txn.Set(db.DeadIndexKey(queueName, ulidStr), nil); err != nil {
					log.Printf("sweep: set dead index for job %s: %v", ulidStr, err)
					continue
				}
				if err := txn.Set(db.JobKey(ulidStr), updatedData); err != nil {
					log.Printf("sweep: update dead job %s: %v", ulidStr, err)
					continue
				}
				log.Printf("sweep: job %s moved to dead-letter (max attempts)", ulidStr)
			} else {
				// Re-queue.
				job.Status = StatusPending
				job.WorkerID = ""
				updatedData, err := json.Marshal(job)
				if err != nil {
					log.Printf("sweep: marshal re-queued job %s: %v", ulidStr, err)
					continue
				}
				if err := txn.Delete(item.Key()); err != nil {
					log.Printf("sweep: delete expired reserved index for job %s: %v", ulidStr, err)
					continue
				}
				if err := txn.Set(db.PendingIndexKey(queueName, ulidStr), nil); err != nil {
					log.Printf("sweep: set pending index for re-queued job %s: %v", ulidStr, err)
					continue
				}
				if err := txn.Set(db.JobKey(ulidStr), updatedData); err != nil {
					log.Printf("sweep: update re-queued job %s: %v", ulidStr, err)
					continue
				}
				log.Printf("sweep: job %s re-queued (visibility timeout expired)", ulidStr)
			}
		}
		return nil
	})
	if err != nil {
		log.Printf("sweep: expire reservations: %v", err)
	}
}

// reconcile checks for orphaned records and fixes inconsistencies.
func (s *Sweeper) reconcile() {
	// Scan all job records with status=reserved and verify matching reserved index exists.
	err := s.db.Update(func(txn *badger.Txn) error {
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
				// Verify reserved index exists.
				_, err := txn.Get(db.ReservedIndexKey(job.Queue, job.ID))
				if err != nil {
					// Orphaned reserved job; re-queue as pending.
					job.Status = StatusPending
					job.WorkerID = ""
					updatedData, err := json.Marshal(job)
					if err != nil {
						log.Printf("sweep: marshal reconciled job %s: %v", job.ID, err)
						continue
					}
					if err := txn.Set(db.JobKey(job.ID), updatedData); err != nil {
						log.Printf("sweep: set reconciled job %s: %v", job.ID, err)
					} else if err := txn.Set(db.PendingIndexKey(job.Queue, job.ID), nil); err != nil {
						log.Printf("sweep: set reconciled pending index for job %s: %v", job.ID, err)
					} else {
						log.Printf("sweep: reconciled orphaned reserved job %s -> pending", job.ID)
					}
				}
			case StatusPending:
				// Verify pending index exists.
				_, err := txn.Get(db.PendingIndexKey(job.Queue, job.ID))
				if err != nil {
					// Re-create missing pending index.
					if err := txn.Set(db.PendingIndexKey(job.Queue, job.ID), nil); err != nil {
						log.Printf("sweep: set missing pending index for job %s: %v", job.ID, err)
					} else {
						log.Printf("sweep: reconciled missing pending index for job %s", job.ID)
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		log.Printf("sweep: reconcile: %v", err)
	}

	// Scan pending indexes and verify matching job records exist.
	err = s.db.Update(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()

		prefix := []byte("queue:")
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			key := string(item.Key())

			if !strings.Contains(key, ":pending:") {
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
				// Phantom index; delete it.
				if err := txn.Delete(item.Key()); err != nil {
					log.Printf("sweep: delete phantom pending index %s: %v", key, err)
				} else {
					log.Printf("sweep: removed phantom pending index %s", key)
				}
			}
		}
		return nil
	})
	if err != nil {
		log.Printf("sweep: reconcile pending indexes: %v", err)
	}
}
