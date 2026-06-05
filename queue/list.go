package queue

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	badger "github.com/dgraph-io/badger/v4"

	"github.com/mkoziy/http-queue/db"
)

const (
	DefaultListLimit = 50
	MaxListLimit     = 1000
)

// ListWorkers returns up to limit workers starting after cursor (exclusive).
// cursor is the ULID of the last item from the previous page; empty means start from the beginning.
// Returns the slice of workers and the next cursor (empty string if no more pages).
func ListWorkers(database *badger.DB, cursor string, limit int) ([]Worker, string, error) {
	var workers []Worker
	var nextCursor string

	err := database.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		it := txn.NewIterator(opts)
		defer it.Close()

		prefix := []byte(db.WorkerPrefix)
		seekKey := prefix
		if cursor != "" {
			seekKey = db.WorkerKey(cursor)
		}

		cursorKey := db.WorkerKey(cursor)

		for it.Seek(seekKey); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()

			// Skip the cursor key itself (exclusive).
			if cursor != "" && string(item.Key()) == string(cursorKey) {
				continue
			}

			if len(workers) == limit {
				nextCursor = workers[len(workers)-1].ID
				break
			}

			data, err := item.ValueCopy(nil)
			if err != nil {
				return fmt.Errorf("read worker: %w", err)
			}

			var w Worker
			if err := json.Unmarshal(data, &w); err != nil {
				return fmt.Errorf("unmarshal worker: %w", err)
			}

			// Merge in-memory last-seen which may be fresher than BadgerDB
			// due to the debounced flush in TouchWorker.
			if ls, ok := workerLastSeen.Load(w.ID); ok {
				if t, ok := ls.(time.Time); ok && t.After(w.LastSeen) {
					w.LastSeen = t
				}
			}

			workers = append(workers, w)
		}

		return nil
	})
	if err != nil {
		return nil, "", fmt.Errorf("list workers: %w", err)
	}

	return workers, nextCursor, nil
}

// ListJobs returns up to limit jobs with the given status in the named queue,
// starting after cursor (exclusive).
// cursor is the ULID of the last item from the previous page; empty means start from the beginning.
// Returns the slice of jobs and the next cursor (empty string if no more pages).
func ListJobs(database *badger.DB, queueName string, status JobStatus, cursor string, limit int) ([]Job, string, error) {
	if err := validateQueueName(queueName); err != nil {
		return nil, "", err
	}

	var prefix []byte
	switch status {
	case StatusPending:
		prefix = db.PendingPrefix(queueName)
	case StatusReserved:
		prefix = db.ReservedPrefix(queueName)
	case StatusDead:
		prefix = db.DeadPrefix(queueName)
	default:
		return nil, "", fmt.Errorf("unknown status %q", status)
	}

	var jobs []Job
	var nextCursor string

	err := database.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		it := txn.NewIterator(opts)
		defer it.Close()

		seekKey := prefix
		var cursorKey []byte
		if cursor != "" {
			cursorKey = append(prefix, []byte(cursor)...)
			seekKey = cursorKey
		}

		for it.Seek(seekKey); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()

			// Skip the cursor key itself (exclusive).
			if cursor != "" && string(item.Key()) == string(cursorKey) {
				continue
			}

			if len(jobs) == limit {
				nextCursor = jobs[len(jobs)-1].ID
				break
			}

			// Extract ULID from index key to load the job record.
			ulidStr := extractULIDFromIndexKey(string(item.Key()), queueName, string(status))
			if ulidStr == "" {
				continue
			}

			jobItem, err := txn.Get(db.JobKey(ulidStr))
			if err != nil {
				// Orphaned index; skip.
				continue
			}

			jobData, err := jobItem.ValueCopy(nil)
			if err != nil {
				return fmt.Errorf("read job: %w", err)
			}

			var j Job
			if err := json.Unmarshal(jobData, &j); err != nil {
				return fmt.Errorf("unmarshal job: %w", err)
			}

			jobs = append(jobs, j)
		}

		return nil
	})
	if err != nil {
		return nil, "", fmt.Errorf("list jobs: %w", err)
	}

	return jobs, nextCursor, nil
}

// ListQueues scans the database and returns a sorted list of all known queue names.
func ListQueues(database *badger.DB) ([]string, error) {
	seen := make(map[string]struct{})

	err := database.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		it := txn.NewIterator(opts)
		defer it.Close()

		prefix := []byte("queue:")
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			// key format: queue:{name}:{status}:{ulid}
			// queue names cannot contain ':', so SplitN(key, ":", 4)[1] is the name.
			parts := strings.SplitN(string(it.Item().Key()), ":", 4)
			if len(parts) >= 2 && parts[1] != "" {
				seen[parts[1]] = struct{}{}
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list queues: %w", err)
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// CountJobs returns the number of jobs with the given status in the named queue.
// Uses a key-only scan (no value fetch) for efficiency.
func CountJobs(database *badger.DB, queueName string, status JobStatus) (int, error) {
	var count int
	err := database.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		it := txn.NewIterator(opts)
		defer it.Close()

		var prefix []byte
		switch status {
		case StatusPending:
			prefix = db.PendingPrefix(queueName)
		case StatusReserved:
			prefix = db.ReservedPrefix(queueName)
		case StatusDead:
			prefix = db.DeadPrefix(queueName)
		default:
			return fmt.Errorf("unknown status: %s", status)
		}

		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			count++
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("count jobs: %w", err)
	}
	return count, nil
}
