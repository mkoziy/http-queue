// Package db provides BadgerDB initialization and key-building helpers.
package db

import (
	"fmt"

	badger "github.com/dgraph-io/badger/v4"
)

// Open opens (or creates) a BadgerDB database at the given path.
func Open(path string) (*badger.DB, error) {
	opts := badger.DefaultOptions(path).
		WithLogger(nil) // silence Badger's own logging for cleaner output

	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("badger open: %w", err)
	}
	return db, nil
}

// Close closes the database after flushing pending writes.
func Close(db *badger.DB) error {
	if err := db.Close(); err != nil {
		return fmt.Errorf("badger close: %w", err)
	}
	return nil
}

// ---- Key builders ----

// JobKey returns the key for a job record: job:{ulid}
func JobKey(ulid string) []byte {
	return []byte("job:" + ulid)
}

// PendingIndexKey returns the pending index key: queue:{queue}:pending:{ulid}
func PendingIndexKey(queue, ulid string) []byte {
	return []byte(fmt.Sprintf("queue:%s:pending:%s", queue, ulid))
}

// ReservedIndexKey returns the reserved index key: queue:{queue}:reserved:{ulid}
// The value stored is the expiry unix timestamp (int64 as string).
func ReservedIndexKey(queue, ulid string) []byte {
	return []byte(fmt.Sprintf("queue:%s:reserved:%s", queue, ulid))
}

// DeadIndexKey returns the dead-letter index key: queue:{queue}:dead:{ulid}
func DeadIndexKey(queue, ulid string) []byte {
	return []byte(fmt.Sprintf("queue:%s:dead:%s", queue, ulid))
}

// WorkerKey returns the key for a worker record: worker:{worker-id}
func WorkerKey(workerID string) []byte {
	return []byte("worker:" + workerID)
}

// WorkerTokenKey returns the reverse-index key for a token hash: workertoken:{sha256-hex}
func WorkerTokenKey(hash string) []byte {
	return []byte("workertoken:" + hash)
}

// QueuePrefix returns the prefix for all keys belonging to a queue.
func QueuePrefix(queue string) []byte {
	return []byte(fmt.Sprintf("queue:%s:", queue))
}

// PendingPrefix returns the prefix for pending index keys in a queue.
func PendingPrefix(queue string) []byte {
	return []byte(fmt.Sprintf("queue:%s:pending:", queue))
}

// ReservedPrefix returns the prefix for reserved index keys in a queue.
func ReservedPrefix(queue string) []byte {
	return []byte(fmt.Sprintf("queue:%s:reserved:", queue))
}

// DeadPrefix returns the prefix for dead-letter index keys in a queue.
func DeadPrefix(queue string) []byte {
	return []byte(fmt.Sprintf("queue:%s:dead:", queue))
}

const (
	// WorkerPrefix is the prefix for worker records.
	WorkerPrefix = "worker:"
	// WorkerTokenPrefix is the prefix for worker token reverse indexes.
	WorkerTokenPrefix = "workertoken:"
)
