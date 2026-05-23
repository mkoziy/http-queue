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
func DeregisterWorker(database *badger.DB, id string) error {
	err := database.Update(func(txn *badger.Txn) error {
		// Load worker to get token hash.
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

		// Re-queue all reserved jobs owned by this worker.
		// We scan reserved indexes and check worker ownership via job records.
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()

		// We need to scan all reserved indexes. In practice this means scanning
		// all queue:*:reserved:* keys. This is a full scan but deregistration
		// is an admin operation, not a hot path.
		prefix := []byte("queue:")
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			key := string(item.Key())

			// Check if it's a reserved index key.
			if !strings.Contains(key, ":reserved:") {
				continue
			}

			// Extract queue name and ULID from the key.
			// Format: queue:{queue}:reserved:{ulid}
			parts := strings.SplitN(key, ":", 4)
			if len(parts) < 4 {
				continue
			}
			queueName := parts[1]
			ulidStr := parts[3]

			// Load the job record to check worker ownership.
			jobItem, jobErr := txn.Get(db.JobKey(ulidStr))
			if jobErr != nil {
				// Orphaned index; delete it.
				_ = txn.Delete(item.Key())
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

			if job.WorkerID != id {
				continue
			}

			// Re-queue: delete reserved index, write pending index, update job.
			if err := txn.Delete(item.Key()); err != nil {
				return fmt.Errorf("delete reserved index: %w", err)
			}

			job.Status = StatusPending
			job.WorkerID = ""
			updatedData, err := json.Marshal(job)
			if err != nil {
				return fmt.Errorf("marshal re-queued job: %w", err)
			}
			if err := txn.Set(db.PendingIndexKey(queueName, ulidStr), nil); err != nil {
				return fmt.Errorf("set pending index: %w", err)
			}
			if err := txn.Set(db.JobKey(ulidStr), updatedData); err != nil {
				return fmt.Errorf("update job: %w", err)
			}
		}

		// Delete token reverse index.
		if err := txn.Delete(db.WorkerTokenKey(w.TokenHash)); err != nil {
			return fmt.Errorf("delete worker token index: %w", err)
		}

		// Delete worker record.
		if err := txn.Delete(db.WorkerKey(id)); err != nil {
			return fmt.Errorf("delete worker: %w", err)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("deregister worker: %w", err)
	}

	// Clean up in-memory last-seen.
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
