// Package queue implements the job and worker storage layer over BadgerDB.
package queue

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	badger "github.com/dgraph-io/badger/v4"
	"github.com/oklog/ulid/v2"

	"github.com/mkoziy/http-queue/db"
)

const maxClaimRetries = 3

// Sentinel errors for job operations.
var (
	// ErrInvalidQueueName is returned when a queue name contains ':'.
	ErrInvalidQueueName = errors.New("queue name must not contain ':'")
	// ErrNotJobOwner is returned when a worker tries to ack/nack a job it doesn't own.
	ErrNotJobOwner = errors.New("job not owned by this worker")
)

// JobStatus represents the lifecycle state of a job.
type JobStatus string

const (
	// StatusPending means the job is waiting to be claimed.
	StatusPending JobStatus = "pending"
	// StatusReserved means the job has been claimed by a worker.
	StatusReserved JobStatus = "reserved"
	// StatusDead means the job exceeded max attempts and is in the dead-letter queue.
	StatusDead JobStatus = "dead"
)

// Job represents a unit of work in a queue.
type Job struct {
	ID        string          `json:"id"`
	Queue     string          `json:"queue"`
	Payload   json.RawMessage `json:"payload"`
	Status    JobStatus       `json:"status"`
	WorkerID  string          `json:"workerID,omitempty"`
	CreatedAt time.Time       `json:"createdAt"`
	Attempts  int             `json:"attempts"`
}

func validateQueueName(queue string) error {
	if strings.Contains(queue, ":") {
		return ErrInvalidQueueName
	}
	return nil
}

// ScheduleJob enqueues a new job into the given queue.
func ScheduleJob(database *badger.DB, queueName string, payload json.RawMessage) (*Job, error) {
	if err := validateQueueName(queueName); err != nil {
		return nil, err
	}

	job := &Job{
		ID:        ulid.Make().String(),
		Queue:     queueName,
		Payload:   payload,
		Status:    StatusPending,
		CreatedAt: time.Now().UTC(),
		Attempts:  0,
	}

	jobData, err := json.Marshal(job)
	if err != nil {
		return nil, fmt.Errorf("marshal job: %w", err)
	}

	err = database.Update(func(txn *badger.Txn) error {
		// Write job record.
		if err := txn.Set(db.JobKey(job.ID), jobData); err != nil {
			return fmt.Errorf("set job: %w", err)
		}
		// Write pending index.
		if err := txn.Set(db.PendingIndexKey(queueName, job.ID), nil); err != nil {
			return fmt.Errorf("set pending index: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("schedule job: %w", err)
	}

	return job, nil
}

// ClaimNextJob claims the next pending job from the queue for a worker.
// Retries on BadgerDB transaction conflicts with a small backoff.
func ClaimNextJob(database *badger.DB, queueName, workerID string, visibilityTimeout time.Duration) (*Job, error) {
	expiry := time.Now().UTC().Add(visibilityTimeout).Unix()

	var claimed *Job
	var lastErr error

	for attempt := 0; attempt < maxClaimRetries; attempt++ {
		claimed = nil

		err := database.Update(func(txn *badger.Txn) error {
			it := txn.NewIterator(badger.DefaultIteratorOptions)
			defer it.Close()

			prefix := db.PendingPrefix(queueName)
			for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
				item := it.Item()
				ulidStr := extractULIDFromIndexKey(string(item.Key()), queueName, "pending")
				if ulidStr == "" {
					continue
				}

				// Read the job record.
				jobItem, err := txn.Get(db.JobKey(ulidStr))
				if err != nil {
					// Orphaned index; delete it.
					_ = txn.Delete(item.Key())
					continue
				}

				jobData, err := jobItem.ValueCopy(nil)
				if err != nil {
					return fmt.Errorf("read job data: %w", err)
				}

				var job Job
				if err := json.Unmarshal(jobData, &job); err != nil {
					return fmt.Errorf("unmarshal job: %w", err)
				}

				// Update job to reserved.
				job.Status = StatusReserved
				job.WorkerID = workerID
				job.Attempts++

				newJobData, err := json.Marshal(job)
				if err != nil {
					return fmt.Errorf("marshal updated job: %w", err)
				}

				// Delete pending index.
				if err := txn.Delete(item.Key()); err != nil {
					return fmt.Errorf("delete pending index: %w", err)
				}

				// Write reserved index with expiry timestamp.
				expiryStr := fmt.Sprintf("%d", expiry)
				if err := txn.Set(db.ReservedIndexKey(queueName, job.ID), []byte(expiryStr)); err != nil {
					return fmt.Errorf("set reserved index: %w", err)
				}

				// Update job record.
				if err := txn.Set(db.JobKey(job.ID), newJobData); err != nil {
					return fmt.Errorf("update job: %w", err)
				}

				claimed = &job
				return nil
			}

			return nil
		})

		if err == nil {
			return claimed, nil
		}

		lastErr = err
		if !errors.Is(err, badger.ErrConflict) {
			break
		}

		// Backoff briefly before retrying.
		time.Sleep(5 * time.Millisecond)
	}

	if lastErr != nil {
		return nil, fmt.Errorf("claim next job: %w", lastErr)
	}

	return claimed, nil
}

// AckJob acknowledges and removes a job from the queue.
func AckJob(database *badger.DB, jobID, workerID string) error {
	err := database.Update(func(txn *badger.Txn) error {
		jobItem, err := txn.Get(db.JobKey(jobID))
		if err != nil {
			return fmt.Errorf("job not found: %w", err)
		}

		jobData, err := jobItem.ValueCopy(nil)
		if err != nil {
			return fmt.Errorf("read job data: %w", err)
		}

		var job Job
		if err := json.Unmarshal(jobData, &job); err != nil {
			return fmt.Errorf("unmarshal job: %w", err)
		}

		if job.WorkerID != workerID {
			return fmt.Errorf("job %s is not owned by worker %s: %w", jobID, workerID, ErrNotJobOwner)
		}

		// Delete reserved index.
		if err := txn.Delete(db.ReservedIndexKey(job.Queue, jobID)); err != nil {
			return fmt.Errorf("delete reserved index: %w", err)
		}

		// Delete job record.
		if err := txn.Delete(db.JobKey(jobID)); err != nil {
			return fmt.Errorf("delete job: %w", err)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("ack job: %w", err)
	}

	return nil
}

// NackJob re-queues a job (or moves to dead-letter if max attempts exceeded).
func NackJob(database *badger.DB, jobID, workerID string, maxAttempts int) error {
	err := database.Update(func(txn *badger.Txn) error {
		jobItem, err := txn.Get(db.JobKey(jobID))
		if err != nil {
			return fmt.Errorf("job not found: %w", err)
		}

		jobData, err := jobItem.ValueCopy(nil)
		if err != nil {
			return fmt.Errorf("read job data: %w", err)
		}

		var job Job
		if err := json.Unmarshal(jobData, &job); err != nil {
			return fmt.Errorf("unmarshal job: %w", err)
		}

		if job.WorkerID != workerID {
			return fmt.Errorf("job %s is not owned by worker %s: %w", jobID, workerID, ErrNotJobOwner)
		}

		// Delete reserved index.
		if err := txn.Delete(db.ReservedIndexKey(job.Queue, jobID)); err != nil {
			return fmt.Errorf("delete reserved index: %w", err)
		}

		if job.Attempts >= maxAttempts {
			// Move to dead-letter.
			job.Status = StatusDead
			job.WorkerID = ""

			newJobData, err := json.Marshal(job)
			if err != nil {
				return fmt.Errorf("marshal dead job: %w", err)
			}

			if err := txn.Set(db.DeadIndexKey(job.Queue, jobID), nil); err != nil {
				return fmt.Errorf("set dead index: %w", err)
			}

			if err := txn.Set(db.JobKey(jobID), newJobData); err != nil {
				return fmt.Errorf("update job: %w", err)
			}
		} else {
			// Re-queue to pending.
			job.Status = StatusPending
			job.WorkerID = ""

			newJobData, err := json.Marshal(job)
			if err != nil {
				return fmt.Errorf("marshal requeued job: %w", err)
			}

			if err := txn.Set(db.PendingIndexKey(job.Queue, jobID), nil); err != nil {
				return fmt.Errorf("set pending index: %w", err)
			}

			if err := txn.Set(db.JobKey(jobID), newJobData); err != nil {
				return fmt.Errorf("update job: %w", err)
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("nack job: %w", err)
	}

	return nil
}

// extractULIDFromIndexKey parses a ULID from an index key like queue:{queue}:pending:{ulid}.
func extractULIDFromIndexKey(key, queue, index string) string {
	prefix := fmt.Sprintf("queue:%s:%s:", queue, index)
	if len(key) < len(prefix) {
		return ""
	}
	return key[len(prefix):]
}

