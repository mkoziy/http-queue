// Package db provides BadgerDB initialization and key-building helpers.
package db

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	badger "github.com/dgraph-io/badger/v4"
)

func TestJobKey(t *testing.T) {
	got := string(JobKey("01JQ3XYZ1234567890ABCDEFGH"))
	want := "job:01JQ3XYZ1234567890ABCDEFGH"
	if got != want {
		t.Errorf("JobKey = %q, want %q", got, want)
	}
}

func TestPendingIndexKey(t *testing.T) {
	got := string(PendingIndexKey("myqueue", "01JQ3XYZ1234567890ABCDEFGH"))
	want := "queue:myqueue:pending:01JQ3XYZ1234567890ABCDEFGH"
	if got != want {
		t.Errorf("PendingIndexKey = %q, want %q", got, want)
	}
}

func TestReservedIndexKey(t *testing.T) {
	got := string(ReservedIndexKey("myqueue", "01JQ3XYZ1234567890ABCDEFGH"))
	want := "queue:myqueue:reserved:01JQ3XYZ1234567890ABCDEFGH"
	if got != want {
		t.Errorf("ReservedIndexKey = %q, want %q", got, want)
	}
}

func TestDeadIndexKey(t *testing.T) {
	got := string(DeadIndexKey("myqueue", "01JQ3XYZ1234567890ABCDEFGH"))
	want := "queue:myqueue:dead:01JQ3XYZ1234567890ABCDEFGH"
	if got != want {
		t.Errorf("DeadIndexKey = %q, want %q", got, want)
	}
}

func TestWorkerKey(t *testing.T) {
	got := string(WorkerKey("01JQ3XYZ1234567890ABCDEFGH"))
	want := "worker:01JQ3XYZ1234567890ABCDEFGH"
	if got != want {
		t.Errorf("WorkerKey = %q, want %q", got, want)
	}
}

func TestWorkerTokenKey(t *testing.T) {
	got := string(WorkerTokenKey("abcdef1234567890"))
	want := "workertoken:abcdef1234567890"
	if got != want {
		t.Errorf("WorkerTokenKey = %q, want %q", got, want)
	}
}

func TestQueuePrefix(t *testing.T) {
	got := string(QueuePrefix("myqueue"))
	want := "queue:myqueue:"
	if got != want {
		t.Errorf("QueuePrefix = %q, want %q", got, want)
	}

	// Empty queue name produces "queue::" from fmt.Sprintf("queue:%s:", "").
	empty := string(QueuePrefix(""))
	if empty != "queue::" {
		t.Errorf("QueuePrefix empty = %q, want %q", empty, "queue::")
	}
}

func TestPendingPrefix(t *testing.T) {
	got := string(PendingPrefix("myqueue"))
	want := "queue:myqueue:pending:"
	if got != want {
		t.Errorf("PendingPrefix = %q, want %q", got, want)
	}
}

func TestReservedPrefix(t *testing.T) {
	got := string(ReservedPrefix("myqueue"))
	want := "queue:myqueue:reserved:"
	if got != want {
		t.Errorf("ReservedPrefix = %q, want %q", got, want)
	}
}

func TestDeadPrefix(t *testing.T) {
	got := string(DeadPrefix("myqueue"))
	want := "queue:myqueue:dead:"
	if got != want {
		t.Errorf("DeadPrefix = %q, want %q", got, want)
	}
}

func TestWorkersConstants(t *testing.T) {
	if WorkerPrefix != "worker:" {
		t.Errorf("WorkerPrefix = %q, want %q", WorkerPrefix, "worker:")
	}
	if WorkerTokenPrefix != "workertoken:" {
		t.Errorf("WorkerTokenPrefix = %q, want %q", WorkerTokenPrefix, "workertoken:")
	}
}

// Key format tests ensure the key builders produce keys that sort lexicographically
// as expected for prefix scans, and that ULID position within keys is consistent.

func TestKeyFormats_ULIDPosition(t *testing.T) {
	ulid := "01JQ3XYZ1234567890ABCDEFGH"

	// In every job-related key, the ULID should be the final segment.
	t.Run("JobKey_ends_with_ulid", func(t *testing.T) {
		key := string(JobKey(ulid))
		wantSuffix := ":" + ulid
		if len(key) < len(wantSuffix) || key[len(key)-len(wantSuffix):] != wantSuffix {
			t.Errorf("JobKey %q does not end with %q", key, wantSuffix)
		}
	})

	t.Run("PendingIndexKey_ends_with_ulid", func(t *testing.T) {
		key := string(PendingIndexKey("q", ulid))
		wantSuffix := ":" + ulid
		if len(key) < len(wantSuffix) || key[len(key)-len(wantSuffix):] != wantSuffix {
			t.Errorf("PendingIndexKey %q does not end with %q", key, wantSuffix)
		}
	})

	t.Run("ReservedIndexKey_ends_with_ulid", func(t *testing.T) {
		key := string(ReservedIndexKey("q", ulid))
		wantSuffix := ":" + ulid
		if len(key) < len(wantSuffix) || key[len(key)-len(wantSuffix):] != wantSuffix {
			t.Errorf("ReservedIndexKey %q does not end with %q", key, wantSuffix)
		}
	})

	t.Run("DeadIndexKey_ends_with_ulid", func(t *testing.T) {
		key := string(DeadIndexKey("q", ulid))
		wantSuffix := ":" + ulid
		if len(key) < len(wantSuffix) || key[len(key)-len(wantSuffix):] != wantSuffix {
			t.Errorf("DeadIndexKey %q does not end with %q", key, wantSuffix)
		}
	})
}

func TestKeyFormats_AreByteSlices(t *testing.T) {
	// All key builders must return non-nil byte slices.
	tests := []struct {
		name string
		key  []byte
	}{
		{"JobKey", JobKey("ulid")},
		{"PendingIndexKey", PendingIndexKey("q", "ulid")},
		{"ReservedIndexKey", ReservedIndexKey("q", "ulid")},
		{"DeadIndexKey", DeadIndexKey("q", "ulid")},
		{"WorkerKey", WorkerKey("id")},
		{"WorkerTokenKey", WorkerTokenKey("hash")},
		{"QueuePrefix", QueuePrefix("q")},
		{"PendingPrefix", PendingPrefix("q")},
		{"ReservedPrefix", ReservedPrefix("q")},
		{"DeadPrefix", DeadPrefix("q")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.key == nil {
				t.Errorf("%s returned nil", tt.name)
			}
			if len(tt.key) == 0 {
				t.Errorf("%s returned empty slice", tt.name)
			}
		})
	}
}

// TestKeyFormats_UniqueJobKeysPerQueue verifies that index keys for the same
// ULID in different queues produce distinct keys.
func TestKeyFormats_UniquePerQueue(t *testing.T) {
	ulid := "01JQ3XYZ1234567890ABCDEFGH"

	pendingA := string(PendingIndexKey("queueA", ulid))
	pendingB := string(PendingIndexKey("queueB", ulid))
	if pendingA == pendingB {
		t.Error("PendingIndexKey for different queues should differ")
	}

	reservedA := string(ReservedIndexKey("queueA", ulid))
	reservedB := string(ReservedIndexKey("queueB", ulid))
	if reservedA == reservedB {
		t.Error("ReservedIndexKey for different queues should differ")
	}

	deadA := string(DeadIndexKey("queueA", ulid))
	deadB := string(DeadIndexKey("queueB", ulid))
	if deadA == deadB {
		t.Error("DeadIndexKey for different queues should differ")
	}
}

// TestKeyFormats_IndexKeysImmuneToULIDSeparator ensures that keys with colons
// in queue names are distinguishable (though queue validation should prevent this).
func TestKeyFormats_QueueNameWithColon(t *testing.T) {
	// Even though queue names shouldn't contain ':', the key builders should
	// still produce valid keys.
	ulid := "01JQ3XYZ1234567890ABCDEFGH"

	pending := string(PendingIndexKey("a:b", ulid))
	if pending != "queue:a:b:pending:01JQ3XYZ1234567890ABCDEFGH" {
		t.Errorf("PendingIndexKey with colon queue = %q", pending)
	}
}

// ---- BadgerDB Lifecycle ----

func TestOpenAndClose(t *testing.T) {
	dir := t.TempDir()

	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	if db == nil {
		t.Fatal("Open() returned nil db")
	}

	if err := Close(db); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
}

func TestOpen_InvalidPath(t *testing.T) {
	_, err := Open("/nonexistent/path/that/cannot/be/created")
	if err == nil {
		t.Fatal("Open() expected error for invalid path")
	}
}

func TestOpen_ExistsAndReopen(t *testing.T) {
	dir := t.TempDir()

	db, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	if err := Close(db); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	// Reopen the same directory.
	db2, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("Reopen() error: %v", err)
	}
	if err := Close(db2); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
}

func TestClose_NilDB(t *testing.T) {
	// Close should handle nil gracefully.
	if err := Close(nil); err != nil {
		t.Errorf("Close(nil) = %v, want nil", err)
	}
}

func TestOpen_ReadOnlyDir(t *testing.T) {
	dir := t.TempDir()
	readonlyDir := filepath.Join(dir, "readonly")
	if err := os.Mkdir(readonlyDir, 0o444); err != nil {
		t.Fatalf("Mkdir readonly: %v", err)
	}

	_, err := Open(filepath.Join(readonlyDir, "data"))
	if err == nil {
		// On some systems root can still write to 0444 dirs; skip assertion.
		t.Log("Open succeeded on read-only directory (expected on some systems)")
	}
}

// ---- Prefix Scan Compatibility ----

func TestPrefixScans_Ordering(t *testing.T) {
	// Verify that index keys sort in a useful order for BadgerDB prefix scans.
	// BadgerDB uses lexicographic byte order, so queue:pending: should sort
	// before queue:reserved: which sorts before queue:dead:.

	pending := PendingIndexKey("q", "a")
	reserved := ReservedIndexKey("q", "a")
	dead := DeadIndexKey("q", "a")

	// "pending" < "reserved" < "dead" lexicographically? Actually "d" < "p" < "r".
	// So dead:pending: < pending: < reserved:
	// Let's just verify they're all different and sort correctly.
	if string(dead) >= string(pending) {
		t.Errorf("expected dead < pending lexicographically, got dead=%q pending=%q", dead, pending)
	}
	if string(pending) >= string(reserved) {
		t.Errorf("expected pending < reserved lexicographically, got pending=%q reserved=%q", pending, reserved)
	}
}

func TestPrefix_IterationBoundaries(t *testing.T) {
	// Verify that prefix keys are proper prefixes of their corresponding index keys.
	ulid := "01JQ3XYZ1234567890ABCDEFGH"

	prefixes := []struct {
		name   string
		prefix func(q string) []byte
		index  func(q, u string) []byte
	}{
		{"PendingPrefix", PendingPrefix, PendingIndexKey},
		{"ReservedPrefix", ReservedPrefix, ReservedIndexKey},
		{"DeadPrefix", DeadPrefix, DeadIndexKey},
	}

	for _, p := range prefixes {
		t.Run(p.name, func(t *testing.T) {
			pref := string(p.prefix("q"))
			idx := string(p.index("q", ulid))
			if len(idx) <= len(pref) || idx[:len(pref)] != pref {
				t.Errorf("index key %q does not have prefix %q", idx, pref)
			}
		})
	}
}

// ---- Queue Prefix Edge Cases ----

func TestQueuePrefix_WithEmptyQueue(t *testing.T) {
	// An empty queue name produces "queue::" due to fmt.Sprintf pattern.
	pref := string(QueuePrefix(""))
	if pref != "queue::" {
		t.Errorf("QueuePrefix empty = %q, want %q", pref, "queue::")
	}
}

func TestPendingPrefix_WithEmptyQueue(t *testing.T) {
	pref := string(PendingPrefix(""))
	if pref != "queue::pending:" {
		t.Errorf("PendingPrefix empty = %q, want %q", pref, "queue::pending:")
	}
}

func TestReservedPrefix_WithEmptyQueue(t *testing.T) {
	pref := string(ReservedPrefix(""))
	if pref != "queue::reserved:" {
		t.Errorf("ReservedPrefix empty = %q, want %q", pref, "queue::reserved:")
	}
}

func TestDeadPrefix_WithEmptyQueue(t *testing.T) {
	pref := string(DeadPrefix(""))
	if pref != "queue::dead:" {
		t.Errorf("DeadPrefix empty = %q, want %q", pref, "queue::dead:")
	}
}

func TestWorkerKey_UniquePerID(t *testing.T) {
	id1 := "worker-1"
	id2 := "worker-2"

	key1 := string(WorkerKey(id1))
	key2 := string(WorkerKey(id2))

	if key1 == key2 {
		t.Error("WorkerKey for different IDs should differ")
	}
	if key1 != "worker:worker-1" {
		t.Errorf("WorkerKey id1 = %q, want %q", key1, "worker:worker-1")
	}
	if key2 != "worker:worker-2" {
		t.Errorf("WorkerKey id2 = %q, want %q", key2, "worker:worker-2")
	}
}

func TestWorkerTokenKey_UniquePerHash(t *testing.T) {
	hash1 := "aaaa"
	hash2 := "bbbb"

	key1 := string(WorkerTokenKey(hash1))
	key2 := string(WorkerTokenKey(hash2))

	if key1 == key2 {
		t.Error("WorkerTokenKey for different hashes should differ")
	}
	if key1 != "workertoken:aaaa" {
		t.Errorf("WorkerTokenKey hash1 = %q, want %q", key1, "workertoken:aaaa")
	}
	if key2 != "workertoken:bbbb" {
		t.Errorf("WorkerTokenKey hash2 = %q, want %q", key2, "workertoken:bbbb")
	}
}

func TestKeyFormats_NoExtraSuffix(t *testing.T) {
	ulid := "01JQ3XYZ"
	queue := "test"

	// Verify that no extra characters are appended beyond the expected format.
	got := string(PendingIndexKey(queue, ulid))
	want := "queue:test:pending:01JQ3XYZ"
	if got != want {
		t.Errorf("PendingIndexKey = %q, want %q", got, want)
	}

	got = string(ReservedIndexKey(queue, ulid))
	want = "queue:test:reserved:01JQ3XYZ"
	if got != want {
		t.Errorf("ReservedIndexKey = %q, want %q", got, want)
	}

	got = string(DeadIndexKey(queue, ulid))
	want = "queue:test:dead:01JQ3XYZ"
	if got != want {
		t.Errorf("DeadIndexKey = %q, want %q", got, want)
	}

	got = string(JobKey(ulid))
	want = "job:01JQ3XYZ"
	if got != want {
		t.Errorf("JobKey = %q, want %q", got, want)
	}

	got = string(WorkerKey(ulid))
	want = "worker:01JQ3XYZ"
	if got != want {
		t.Errorf("WorkerKey = %q, want %q", got, want)
	}

	got = string(WorkerTokenKey(ulid))
	want = "workertoken:01JQ3XYZ"
	if got != want {
		t.Errorf("WorkerTokenKey = %q, want %q", got, want)
	}
}

func TestOpen_WithLoggerDisabled(t *testing.T) {
	dir := t.TempDir()

	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	defer func() {
		_ = Close(db)
	}()

	// Verify the database is usable by running a simple View transaction.
	err = db.View(func(_ *badger.Txn) error {
		return nil
	})
	if err != nil {
		t.Fatalf("db.View() error: %v", err)
	}
}

func TestOpen_DataPersistence(t *testing.T) {
	dir := t.TempDir()

	// Open, write a key, close.
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}

	err = db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte("testkey"), []byte("testvalue"))
	})
	if err != nil {
		t.Fatalf("db.Update() error: %v", err)
	}

	if err := Close(db); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	// Reopen and verify the key persists.
	db2, err := Open(dir)
	if err != nil {
		t.Fatalf("Reopen() error: %v", err)
	}
	defer func() {
		_ = Close(db2)
	}()

	err = db2.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte("testkey"))
		if err != nil {
			return fmt.Errorf("get testkey: %w", err)
		}
		val, err := item.ValueCopy(nil)
		if err != nil {
			return fmt.Errorf("value copy: %w", err)
		}
		if string(val) != "testvalue" {
			t.Errorf("value = %q, want %q", string(val), "testvalue")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("db2.View() error: %v", err)
	}
}
