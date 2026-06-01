package queue

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	badger "github.com/dgraph-io/badger/v4"
	"github.com/oklog/ulid/v2"

	"github.com/mkoziy/http-queue/config"
	"github.com/mkoziy/http-queue/db"
	"github.com/mkoziy/http-queue/token"
)

// Worker represents a registered worker process.
type Worker struct {
	ID           string    `json:"id"`
	TokenHash    string    `json:"tokenHash"`
	LastSeen     time.Time `json:"lastSeen"`
	RegisteredAt time.Time `json:"registeredAt"`
}

// Workers memory for debounced last-seen tracking.
// Key: worker ID, Value: last-seen time.Time.
var workerLastSeen sync.Map

// RegisterWorker creates a new worker, returning its ID and plaintext bearer token.
func RegisterWorker(database *badger.DB, _ *config.Config) (id, plainToken string, err error) {
	id = ulid.Make().String()
	plainToken, hashedToken, err := token.Generate()
	if err != nil {
		return "", "", fmt.Errorf("register worker: %w", err)
	}

	now := time.Now().UTC()
	worker := &Worker{
		ID:           id,
		TokenHash:    hashedToken,
		LastSeen:     now,
		RegisteredAt: now,
	}

	workerData, err := json.Marshal(worker)
	if err != nil {
		return "", "", fmt.Errorf("marshal worker: %w", err)
	}

	err = database.Update(func(txn *badger.Txn) error {
		// Write worker record.
		if err := txn.Set(db.WorkerKey(id), workerData); err != nil {
			return fmt.Errorf("set worker: %w", err)
		}
		// Write token reverse index.
		if err := txn.Set(db.WorkerTokenKey(hashedToken), []byte(id)); err != nil {
			return fmt.Errorf("set worker token index: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", "", fmt.Errorf("register worker: %w", err)
	}

	// Initialize in-memory last-seen.
	workerLastSeen.Store(id, now)

	return id, plainToken, nil
}

// DeregisterWorker removes a worker and re-queues its reserved jobs.
// Reserved jobs are re-queued in bounded write transactions to prevent
// unbounded key mutations in a single BadgerDB transaction.
func DeregisterWorker(database *badger.DB, id string) error {
	// Step 1: Load worker token hash in a read transaction.
	tokenHash, err := loadWorkerTokenHash(database, id)
	if err != nil {
		return fmt.Errorf("deregister worker: %w", err)
	}

	// Step 2: Collect owned reserved job refs in a read transaction.
	refs, err := collectOwnedReservedRefs(database, id)
	if err != nil {
		return fmt.Errorf("deregister worker: %w", err)
	}

	// Step 3: Re-queue owned reservations in bounded write transactions.
	for batchIdx, batch := range batchSlice(refs, maintenanceBatchSize) {
		if err := requeueReservedBatch(database, id, batch); err != nil {
			return fmt.Errorf("deregister worker: batch %d: %w", batchIdx, err)
		}
	}

	// Step 4: Delete worker record and token index only after all
	// reservation batches complete successfully, so a failed partial
	// deregistration remains retryable.
	err = database.Update(func(txn *badger.Txn) error {
		if err := txn.Delete(db.WorkerTokenKey(tokenHash)); err != nil {
			return fmt.Errorf("delete worker token index: %w", err)
		}
		if err := txn.Delete(db.WorkerKey(id)); err != nil {
			return fmt.Errorf("delete worker: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("deregister worker: %w", err)
	}

	// Step 5: Clean up in-memory cache only after full success.
	workerLastSeen.Delete(id)
	workerLastSeen.Delete("flush:" + id)

	return nil
}

// TouchWorker updates the worker's last-seen timestamp.
// The in-memory cache is always updated; BadgerDB write is debounced
// to avoid write amplification on the poll hot path.
func TouchWorker(database *badger.DB, id string, debounce time.Duration) {
	now := time.Now().UTC()

	// Always update in-memory.
	workerLastSeen.Store(id, now)

	// Check if we should flush to BadgerDB.
	if lastFlush, ok := workerLastSeen.Load("flush:" + id); ok {
		if lastFlushTime, ok := lastFlush.(time.Time); ok && now.Sub(lastFlushTime) < debounce {
			return
		}
	}

	// Flush to BadgerDB.
	if err := database.Update(func(txn *badger.Txn) error {
		item, err := txn.Get(db.WorkerKey(id))
		if err != nil {
			return fmt.Errorf("get worker: %w", err)
		}

		data, err := item.ValueCopy(nil)
		if err != nil {
			return fmt.Errorf("read worker data: %w", err)
		}

		var w Worker
		if err := json.Unmarshal(data, &w); err != nil {
			return fmt.Errorf("unmarshal worker: %w", err)
		}

		w.LastSeen = now

		updated, err := json.Marshal(w)
		if err != nil {
			return fmt.Errorf("marshal worker: %w", err)
		}

		return txn.Set(db.WorkerKey(id), updated)
	}); err != nil {
		// Log but don't fail — the in-memory cache is still up-to-date
		// and the next call will retry because the flush time wasn't recorded.
		log.Printf("touch worker %s: flush to db: %v", id, err)
		return
	}

	// Record flush time only on success so failures are retried on next call.
	workerLastSeen.Store("flush:"+id, now)
}

// WorkerByToken looks up a worker by its bearer token.
func WorkerByToken(database *badger.DB, plainToken string) (*Worker, error) {
	hashedToken := token.Hash(plainToken)

	var w *Worker
	err := database.View(func(txn *badger.Txn) error {
		// Look up the reverse index.
		indexItem, err := txn.Get(db.WorkerTokenKey(hashedToken))
		if err != nil {
			return fmt.Errorf("token not found: %w", err)
		}

		workerID, err := indexItem.ValueCopy(nil)
		if err != nil {
			return fmt.Errorf("read token index: %w", err)
		}

		// Load worker record.
		workerItem, err := txn.Get(db.WorkerKey(string(workerID)))
		if err != nil {
			return fmt.Errorf("worker not found: %w", err)
		}

		workerData, err := workerItem.ValueCopy(nil)
		if err != nil {
			return fmt.Errorf("read worker data: %w", err)
		}

		var worker Worker
		if err := json.Unmarshal(workerData, &worker); err != nil {
			return fmt.Errorf("unmarshal worker: %w", err)
		}

		w = &worker
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("worker by token: %w", err)
	}

	return w, nil
}

// reservedRef identifies a reserved job owned by a specific worker.
type reservedRef struct {
	queue string
	ulid  string
}

// loadWorkerTokenHash reads a worker's token hash from the database
// in a read transaction.
func loadWorkerTokenHash(database *badger.DB, id string) (string, error) {
	var tokenHash string
	err := database.View(func(txn *badger.Txn) error {
		workerItem, err := txn.Get(db.WorkerKey(id))
		if err != nil {
			return fmt.Errorf("worker not found: %w", err)
		}
		workerData, err := workerItem.ValueCopy(nil)
		if err != nil {
			return fmt.Errorf("read worker data: %w", err)
		}
		var w Worker
		if err := json.Unmarshal(workerData, &w); err != nil {
			return fmt.Errorf("unmarshal worker: %w", err)
		}
		tokenHash = w.TokenHash
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("load worker token hash: %w", err)
	}
	return tokenHash, nil
}

// collectOwnedReservedRefs scans all reserved indexes and collects refs
// for jobs owned by the given worker. Orphaned reserved indexes (no matching
// job record) are also collected so they can be cleaned up in the batch
// requeue phase. Returns an empty slice if none found.
func collectOwnedReservedRefs(database *badger.DB, workerID string) ([]reservedRef, error) {
	var refs []reservedRef
	err := database.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()

		prefix := []byte("queue:")
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			key := string(item.Key())

			if !strings.Contains(key, ":reserved:") {
				continue
			}

			parts := strings.SplitN(key, ":", 4)
			if len(parts) < 4 {
				continue
			}
			queueName := parts[1]
			ulidStr := parts[3]

			// Load the job record to check worker ownership.
			jobItem, jobErr := txn.Get(db.JobKey(ulidStr))
			if jobErr != nil {
				// Orphaned reserved index (job record missing); collect it for
				// cleanup in the batch requeue phase.
				refs = append(refs, reservedRef{queue: queueName, ulid: ulidStr})
				continue
			}

			jobData, jobErr := jobItem.ValueCopy(nil)
			if jobErr != nil {
				continue
			}

			var job Job
			if err := json.Unmarshal(jobData, &job); err != nil {
				continue
			}

			if job.WorkerID != workerID {
				continue
			}

			refs = append(refs, reservedRef{queue: queueName, ulid: ulidStr})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("collect owned reserved refs: %w", err)
	}
	return refs, nil
}

// requeueReservedBatch re-queues a batch of reserved job refs in a single
// write transaction. Each ref is re-checked: the job must still exist, be
// in Reserved status, and be owned by the given worker. Stale or orphaned
// reserved indexes are cleaned up.
func requeueReservedBatch(database *badger.DB, workerID string, refs []reservedRef) error {
	if len(refs) == 0 {
		return nil
	}

	now := time.Now().UTC()
	err := database.Update(func(txn *badger.Txn) error {
		for _, ref := range refs {
			jobItem, err := txn.Get(db.JobKey(ref.ulid))
			if err != nil {
				// Job record missing; delete orphaned reserved index.
				_ = txn.Delete(db.ReservedIndexKey(ref.queue, ref.ulid))
				continue
			}

			jobData, err := jobItem.ValueCopy(nil)
			if err != nil {
				continue
			}

			var j Job
			if err := json.Unmarshal(jobData, &j); err != nil {
				continue
			}

			// Re-check the job is still reserved and owned by this worker.
			if j.Status != StatusReserved || j.WorkerID != workerID {
				continue
			}

			if isJobExpired(j, now) {
				if err := deleteJobWithIndexes(txn, j, db.ReservedIndexKey(ref.queue, ref.ulid)); err != nil {
					return fmt.Errorf("delete expired reserved job %s: %w", ref.ulid, err)
				}
				continue
			}

			// Re-queue: delete reserved index, write pending index, update job.
			if err := txn.Delete(db.ReservedIndexKey(ref.queue, ref.ulid)); err != nil {
				return fmt.Errorf("delete reserved index for %s: %w", ref.ulid, err)
			}

			j.Status = StatusPending
			j.WorkerID = ""
			updatedData, err := json.Marshal(j)
			if err != nil {
				return fmt.Errorf("marshal re-queued job %s: %w", ref.ulid, err)
			}
			if err := txn.Set(db.PendingIndexKey(ref.queue, ref.ulid), nil); err != nil {
				return fmt.Errorf("set pending index for %s: %w", ref.ulid, err)
			}
			if err := txn.Set(db.JobKey(ref.ulid), updatedData); err != nil {
				return fmt.Errorf("update job %s: %w", ref.ulid, err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("requeue reserved batch: %w", err)
	}
	return nil
}
