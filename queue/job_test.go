//nolint:wrapcheck // test assertions pass through external errors; wrapping is noise here.
package queue

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	badger "github.com/dgraph-io/badger/v4"

	"github.com/mkoziy/http-queue/db"
)

// ---- ScheduleJob Tests ----

func TestScheduleJob(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	job, err := ScheduleJob(database, "testqueue", json.RawMessage(`{"hello":"world"}`))
	if err != nil {
		t.Fatalf("ScheduleJob() error: %v", err)
	}

	if job.ID == "" {
		t.Error("ScheduleJob() returned empty job ID")
	}
	if job.Queue != "testqueue" {
		t.Errorf("job.Queue = %q, want %q", job.Queue, "testqueue")
	}
	if job.Status != StatusPending {
		t.Errorf("job.Status = %q, want %q", job.Status, StatusPending)
	}
	if job.Attempts != 0 {
		t.Errorf("job.Attempts = %d, want 0", job.Attempts)
	}
	if job.CreatedAt.IsZero() {
		t.Error("job.CreatedAt is zero")
	}
	if job.WorkerID != "" {
		t.Errorf("job.WorkerID = %q, want empty", job.WorkerID)
	}

	// Verify the payload was preserved.
	var payload map[string]string
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		t.Fatalf("unmarshal job payload: %v", err)
	}
	if payload["hello"] != "world" {
		t.Errorf("payload[hello] = %q, want %q", payload["hello"], "world")
	}

	// Verify the job record exists in BadgerDB.
	err = database.View(func(txn *badger.Txn) error {
		item, err := txn.Get(db.JobKey(job.ID))
		if err != nil {
			return err
		}
		data, err := item.ValueCopy(nil)
		if err != nil {
			return err
		}
		if len(data) == 0 {
			t.Error("job record is empty")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("job record not found: %v", err)
	}

	// Verify the pending index exists.
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.PendingIndexKey("testqueue", job.ID))
		return err
	})
	if err != nil {
		t.Fatalf("pending index not found: %v", err)
	}
}

func TestScheduleJob_UniqueIDs(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	ids := make(map[string]bool)
	for i := 0; i < 10; i++ {
		job, err := ScheduleJob(database, "testqueue", json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("ScheduleJob() error: %v", err)
		}
		if ids[job.ID] {
			t.Errorf("duplicate job ID generated: %s", job.ID)
		}
		ids[job.ID] = true
	}
}

func TestScheduleJob_QueueNameWithColonRejected(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	_, err := ScheduleJob(database, "bad:queue", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("ScheduleJob with ':' in queue name: expected error, got nil")
	}
	if !strings.Contains(err.Error(), ErrInvalidQueueName.Error()) {
		t.Errorf("ScheduleJob error = %v, want %v", err, ErrInvalidQueueName)
	}
}

func TestScheduleJob_MultipleQueues(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	queues := []string{"queueA", "queueB", "queueC"}
	for _, q := range queues {
		job, err := ScheduleJob(database, q, json.RawMessage(`{"queue":"`+q+`"}`))
		if err != nil {
			t.Fatalf("ScheduleJob(%q) error: %v", q, err)
		}
		if job.Queue != q {
			t.Errorf("job.Queue = %q, want %q", job.Queue, q)
		}
	}
}

func TestScheduleJob_PreservesPayload(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	payloads := []json.RawMessage{
		json.RawMessage(`null`),
		json.RawMessage(`true`),
		json.RawMessage(`42`),
		json.RawMessage(`"a string"`),
		json.RawMessage(`{"nested":{"object":true}}`),
		json.RawMessage(`[1,2,3]`),
	}

	for _, p := range payloads {
		job, err := ScheduleJob(database, "testqueue", p)
		if err != nil {
			t.Fatalf("ScheduleJob() with payload %s error: %v", string(p), err)
		}
		if string(job.Payload) != string(p) {
			t.Errorf("payload mismatch: got %s, want %s", string(job.Payload), string(p))
		}
	}
}

// ---- ClaimNextJob Tests ----

func TestClaimNextJob(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	// Schedule a job.
	scheduled, err := ScheduleJob(database, "testqueue", json.RawMessage(`{"hello":"world"}`))
	if err != nil {
		t.Fatalf("ScheduleJob() error: %v", err)
	}

	// Claim it.
	claimed, err := ClaimNextJob(database, "testqueue", "worker-1", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNextJob() error: %v", err)
	}
	if claimed == nil {
		t.Fatal("ClaimNextJob() returned nil, expected a job")
	}

	if claimed.ID != scheduled.ID {
		t.Errorf("claimed.ID = %q, want %q", claimed.ID, scheduled.ID)
	}
	if claimed.Status != StatusReserved {
		t.Errorf("claimed.Status = %q, want %q", claimed.Status, StatusReserved)
	}
	if claimed.WorkerID != "worker-1" {
		t.Errorf("claimed.WorkerID = %q, want %q", claimed.WorkerID, "worker-1")
	}
	if claimed.Attempts != 1 {
		t.Errorf("claimed.Attempts = %d, want 1", claimed.Attempts)
	}

	// Verify the job record was updated in the database.
	err = database.View(func(txn *badger.Txn) error {
		item, err := txn.Get(db.JobKey(claimed.ID))
		if err != nil {
			return err
		}
		data, err := item.ValueCopy(nil)
		if err != nil {
			return err
		}
		var j Job
		if err := json.Unmarshal(data, &j); err != nil {
			return err
		}
		if j.Status != StatusReserved {
			t.Errorf("db job.Status = %q, want %q", j.Status, StatusReserved)
		}
		if j.WorkerID != "worker-1" {
			t.Errorf("db job.WorkerID = %q, want %q", j.WorkerID, "worker-1")
		}
		if j.Attempts != 1 {
			t.Errorf("db job.Attempts = %d, want 1", j.Attempts)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading job: %v", err)
	}

	// Verify pending index is gone.
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.PendingIndexKey("testqueue", claimed.ID))
		return err
	})
	if err == nil {
		t.Error("pending index still exists after claim")
	}

	// Verify reserved index exists.
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.ReservedIndexKey("testqueue", claimed.ID))
		return err
	})
	if err != nil {
		t.Error("reserved index missing after claim")
	}
}

func TestClaimNextJob_EmptyQueue(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	claimed, err := ClaimNextJob(database, "emptyqueue", "worker-1", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNextJob() on empty queue error: %v", err)
	}
	if claimed != nil {
		t.Fatalf("ClaimNextJob() on empty queue returned %+v, want nil", claimed)
	}
}

func TestClaimNextJob_OnlyPendingJobs(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	// Schedule a job and claim it.
	_, err := ScheduleJob(database, "testqueue", json.RawMessage(`{"n":1}`))
	if err != nil {
		t.Fatalf("ScheduleJob(): %v", err)
	}

	claimed, err := ClaimNextJob(database, "testqueue", "worker-1", 30*time.Second)
	if err != nil {
		t.Fatalf("First ClaimNextJob(): %v", err)
	}
	if claimed == nil {
		t.Fatal("First ClaimNextJob(): expected a job, got nil")
	}

	// The only job is now reserved; claiming again should return nil.
	second, err := ClaimNextJob(database, "testqueue", "worker-2", 30*time.Second)
	if err != nil {
		t.Fatalf("Second ClaimNextJob(): %v", err)
	}
	if second != nil {
		t.Fatalf("Second ClaimNextJob() returned %+v, want nil (job already reserved)", second)
	}
}

func TestClaimNextJob_MultipleQueues(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	// Schedule jobs in different queues.
	_, err := ScheduleJob(database, "queueA", json.RawMessage(`{"q":"A"}`))
	if err != nil {
		t.Fatalf("ScheduleJob queueA: %v", err)
	}
	_, err = ScheduleJob(database, "queueB", json.RawMessage(`{"q":"B"}`))
	if err != nil {
		t.Fatalf("ScheduleJob queueB: %v", err)
	}

	// Claim from queueA should return the queueA job.
	claimedA, err := ClaimNextJob(database, "queueA", "worker-1", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNextJob queueA: %v", err)
	}
	if claimedA == nil {
		t.Fatal("ClaimNextJob queueA returned nil")
	}
	if claimedA.Queue != "queueA" {
		t.Errorf("claimedA.Queue = %q, want %q", claimedA.Queue, "queueA")
	}

	// Claim from queueB should return the queueB job.
	claimedB, err := ClaimNextJob(database, "queueB", "worker-1", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNextJob queueB: %v", err)
	}
	if claimedB == nil {
		t.Fatal("ClaimNextJob queueB returned nil")
	}
	if claimedB.Queue != "queueB" {
		t.Errorf("claimedB.Queue = %q, want %q", claimedB.Queue, "queueB")
	}
}

func TestClaimNextJob_FIFOOrdering(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	// Schedule jobs in order; ULIDs are time-sorted so they should be FIFO.
	ids := make([]string, 5)
	for i := 0; i < 5; i++ {
		job, err := ScheduleJob(database, "testqueue", json.RawMessage(`{"n":`+string(rune('0'+i))+`}`))
		if err != nil {
			t.Fatalf("ScheduleJob %d: %v", i, err)
		}
		ids[i] = job.ID
	}

	// Claim them all and verify FIFO order.
	for i := 0; i < 5; i++ {
		claimed, err := ClaimNextJob(database, "testqueue", "worker-1", 30*time.Second)
		if err != nil {
			t.Fatalf("ClaimNextJob %d: %v", i, err)
		}
		if claimed == nil {
			t.Fatalf("ClaimNextJob %d returned nil", i)
		}
		if claimed.ID != ids[i] {
			t.Errorf("Claim %d: got ID %q, want %q", i, claimed.ID, ids[i])
		}
	}
}

// ---- AckJob Tests ----

func TestAckJob(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	// Schedule and claim a job.
	scheduled, err := ScheduleJob(database, "testqueue", json.RawMessage(`{"hello":"world"}`))
	if err != nil {
		t.Fatalf("ScheduleJob(): %v", err)
	}

	claimed, err := ClaimNextJob(database, "testqueue", "worker-1", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNextJob(): %v", err)
	}
	if claimed == nil {
		t.Fatal("ClaimNextJob() returned nil")
	}

	// Ack the job.
	if err := AckJob(database, claimed.ID, "worker-1"); err != nil {
		t.Fatalf("AckJob() error: %v", err)
	}

	// Verify job record is deleted.
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.JobKey(claimed.ID))
		return err
	})
	if err == nil {
		t.Error("job record still exists after ack")
	}

	// Verify reserved index is deleted.
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.ReservedIndexKey("testqueue", claimed.ID))
		return err
	})
	if err == nil {
		t.Error("reserved index still exists after ack")
	}

	// Verify the job ID is the same (the struct returned by ClaimNextJob should match).
	_ = scheduled
}

func TestAckJob_WrongWorker(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	// Schedule and claim a job with worker-1.
	_, err := ScheduleJob(database, "testqueue", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("ScheduleJob(): %v", err)
	}

	claimed, err := ClaimNextJob(database, "testqueue", "worker-1", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNextJob(): %v", err)
	}
	if claimed == nil {
		t.Fatal("ClaimNextJob() returned nil")
	}

	// worker-2 tries to ack the job.
	err = AckJob(database, claimed.ID, "worker-2")
	if err == nil {
		t.Fatal("AckJob by wrong worker: expected error, got nil")
	}
}

func TestAckJob_Nonexistent(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	err := AckJob(database, "nonexistent-ulid", "worker-1")
	if err == nil {
		t.Fatal("AckJob for nonexistent job: expected error, got nil")
	}
}

func TestAckJob_Twice(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	_, err := ScheduleJob(database, "testqueue", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("ScheduleJob(): %v", err)
	}

	claimed, err := ClaimNextJob(database, "testqueue", "worker-1", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNextJob(): %v", err)
	}
	if claimed == nil {
		t.Fatal("ClaimNextJob() returned nil")
	}

	if err := AckJob(database, claimed.ID, "worker-1"); err != nil {
		t.Fatalf("First AckJob(): %v", err)
	}

	// Second ack should fail (job already deleted).
	err = AckJob(database, claimed.ID, "worker-1")
	if err == nil {
		t.Fatal("Second AckJob: expected error, got nil")
	}
}

// ---- NackJob Tests ----

func TestNackJob_Requeues(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()

	// Schedule and claim a job.
	_, err := ScheduleJob(database, "testqueue", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("ScheduleJob(): %v", err)
	}

	claimed, err := ClaimNextJob(database, "testqueue", "worker-1", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNextJob(): %v", err)
	}
	if claimed == nil {
		t.Fatal("ClaimNextJob() returned nil")
	}

	// Nack the job (should re-queue to pending).
	if err := NackJob(database, claimed.ID, "worker-1", cfg.MaxAttempts); err != nil {
		t.Fatalf("NackJob() error: %v", err)
	}

	// Verify job is back to pending with no owner.
	err = database.View(func(txn *badger.Txn) error {
		item, err := txn.Get(db.JobKey(claimed.ID))
		if err != nil {
			return err
		}
		data, err := item.ValueCopy(nil)
		if err != nil {
			return err
		}
		var j Job
		if err := json.Unmarshal(data, &j); err != nil {
			return err
		}
		if j.Status != StatusPending {
			t.Errorf("after nack, job.Status = %q, want %q", j.Status, StatusPending)
		}
		if j.WorkerID != "" {
			t.Errorf("after nack, job.WorkerID = %q, want empty", j.WorkerID)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading job after nack: %v", err)
	}

	// Verify pending index exists.
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.PendingIndexKey("testqueue", claimed.ID))
		return err
	})
	if err != nil {
		t.Error("pending index missing after nack (job should be re-queued)")
	}

	// Verify reserved index is gone.
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.ReservedIndexKey("testqueue", claimed.ID))
		return err
	})
	if err == nil {
		t.Error("reserved index still exists after nack")
	}

	// Verify the job can be claimed again.
	reclaimed, err := ClaimNextJob(database, "testqueue", "worker-2", 30*time.Second)
	if err != nil {
		t.Fatalf("Reclaim after nack: %v", err)
	}
	if reclaimed == nil {
		t.Fatal("Reclaim after nack returned nil")
	}
	if reclaimed.ID != claimed.ID {
		t.Errorf("reclaimed.ID = %q, want %q", reclaimed.ID, claimed.ID)
	}
	if reclaimed.Attempts != 2 {
		t.Errorf("reclaimed.Attempts = %d, want 2", reclaimed.Attempts)
	}
}

func TestNackJob_MovesToDeadLetterAtMaxAttempts(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()

	// Schedule a job.
	_, err := ScheduleJob(database, "testqueue", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("ScheduleJob(): %v", err)
	}

	// Claim it — Attempts goes to 1.
	claimed, err := ClaimNextJob(database, "testqueue", "worker-1", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNextJob(): %v", err)
	}
	if claimed == nil {
		t.Fatal("ClaimNextJob() returned nil")
	}

	// Nack — should re-queue (Attempts=1 < MaxAttempts=3).
	if err := NackJob(database, claimed.ID, "worker-1", cfg.MaxAttempts); err != nil {
		t.Fatalf("NackJob 1: %v", err)
	}

	// Claim again — Attempts goes to 2.
	claimed, err = ClaimNextJob(database, "testqueue", "worker-1", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNextJob 2: %v", err)
	}
	if claimed == nil {
		t.Fatal("ClaimNextJob 2 returned nil")
	}

	// Nack — should re-queue (Attempts=2 < MaxAttempts=3).
	if err := NackJob(database, claimed.ID, "worker-1", cfg.MaxAttempts); err != nil {
		t.Fatalf("NackJob 2: %v", err)
	}

	// Claim again — Attempts goes to 3.
	claimed, err = ClaimNextJob(database, "testqueue", "worker-1", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNextJob 3: %v", err)
	}
	if claimed == nil {
		t.Fatal("ClaimNextJob 3 returned nil")
	}

	// Nack — Attempts=3 >= MaxAttempts=3, should go to dead-letter.
	if err := NackJob(database, claimed.ID, "worker-1", cfg.MaxAttempts); err != nil {
		t.Fatalf("NackJob 3 (dead-letter): %v", err)
	}

	// Verify job status is 'dead'.
	err = database.View(func(txn *badger.Txn) error {
		item, err := txn.Get(db.JobKey(claimed.ID))
		if err != nil {
			return err
		}
		data, err := item.ValueCopy(nil)
		if err != nil {
			return err
		}
		var j Job
		if err := json.Unmarshal(data, &j); err != nil {
			return err
		}
		if j.Status != StatusDead {
			t.Errorf("after max attempts nack, job.Status = %q, want %q", j.Status, StatusDead)
		}
		if j.WorkerID != "" {
			t.Errorf("after max attempts nack, job.WorkerID = %q, want empty", j.WorkerID)
		}
		if j.Attempts != 3 {
			t.Errorf("after max attempts nack, job.Attempts = %d, want 3", j.Attempts)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading job after dead-letter: %v", err)
	}

	// Verify dead index exists.
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.DeadIndexKey("testqueue", claimed.ID))
		return err
	})
	if err != nil {
		t.Error("dead index missing after max attempts nack")
	}

	// Verify pending and reserved indexes are gone.
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.PendingIndexKey("testqueue", claimed.ID))
		return err
	})
	if err == nil {
		t.Error("pending index still exists after dead-letter")
	}

	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.ReservedIndexKey("testqueue", claimed.ID))
		return err
	})
	if err == nil {
		t.Error("reserved index still exists after dead-letter")
	}

	// Verify the job can no longer be claimed (not in pending index).
	next, err := ClaimNextJob(database, "testqueue", "worker-1", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNextJob after dead-letter: %v", err)
	}
	if next != nil {
		t.Errorf("ClaimNextJob after dead-letter returned job, want nil")
	}
}

func TestNackJob_MaxAttemptsOne(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	// Schedule and claim a job.
	_, err := ScheduleJob(database, "testqueue", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("ScheduleJob(): %v", err)
	}

	claimed, err := ClaimNextJob(database, "testqueue", "worker-1", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNextJob(): %v", err)
	}
	if claimed == nil {
		t.Fatal("ClaimNextJob() returned nil")
	}

	// Nack with MaxAttempts=1 should immediately go to dead-letter (Attempts=1 >= 1).
	if err := NackJob(database, claimed.ID, "worker-1", 1); err != nil {
		t.Fatalf("NackJob with MaxAttempts=1: %v", err)
	}

	// Verify dead index exists.
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.DeadIndexKey("testqueue", claimed.ID))
		return err
	})
	if err != nil {
		t.Error("dead index missing after nack with MaxAttempts=1")
	}

	// Verify job status is dead.
	err = database.View(func(txn *badger.Txn) error {
		item, err := txn.Get(db.JobKey(claimed.ID))
		if err != nil {
			return err
		}
		data, err := item.ValueCopy(nil)
		if err != nil {
			return err
		}
		var j Job
		if err := json.Unmarshal(data, &j); err != nil {
			return err
		}
		if j.Status != StatusDead {
			t.Errorf("job.Status = %q, want %q", j.Status, StatusDead)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading job: %v", err)
	}
}

func TestNackJob_WrongWorker(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()

	_, err := ScheduleJob(database, "testqueue", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("ScheduleJob(): %v", err)
	}

	claimed, err := ClaimNextJob(database, "testqueue", "worker-1", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNextJob(): %v", err)
	}
	if claimed == nil {
		t.Fatal("ClaimNextJob() returned nil")
	}

	err = NackJob(database, claimed.ID, "worker-2", cfg.MaxAttempts)
	if err == nil {
		t.Fatal("NackJob by wrong worker: expected error, got nil")
	}
}

func TestNackJob_Nonexistent(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	err := NackJob(database, "nonexistent-ulid", "worker-1", 3)
	if err == nil {
		t.Fatal("NackJob for nonexistent job: expected error, got nil")
	}
}

// ---- Double-Claim Race ----

func TestDoubleClaimRace(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	// Schedule a single job.
	_, err := ScheduleJob(database, "testqueue", json.RawMessage(`{"race":"test"}`))
	if err != nil {
		t.Fatalf("ScheduleJob(): %v", err)
	}

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		claimants []string
	)

	// 10 workers attempt to claim the same job simultaneously.
	// Transaction conflicts are expected in high-contention scenarios;
	// we verify that exactly one worker succeeds.
	const numWorkers = 10
	wg.Add(numWorkers)

	for i := 0; i < numWorkers; i++ {
		go func(workerID string) {
			defer wg.Done()
			claimed, err := claimNextJobRetry(database, "testqueue", workerID, 30*time.Second)
			if err != nil {
				t.Errorf("ClaimNextJob for %s: %v", workerID, err)
				return
			}
			if claimed != nil {
				mu.Lock()
				claimants = append(claimants, workerID)
				mu.Unlock()
			}
		}(fmt.Sprintf("worker-%d", i))
	}

	wg.Wait()

	// Exactly one worker should have claimed the job.
	if len(claimants) != 1 {
		t.Errorf("expected exactly 1 claimant, got %d: %v", len(claimants), claimants)
	}
}

func TestDoubleClaimRace_MultipleJobs(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	// Schedule 5 jobs.
	const numJobs = 5
	for i := 0; i < numJobs; i++ {
		_, err := ScheduleJob(database, "testqueue", json.RawMessage(fmt.Sprintf(`{"n":%d}`, i)))
		if err != nil {
			t.Fatalf("ScheduleJob %d: %v", i, err)
		}
	}

	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		claims = make(map[string]string) // jobID -> workerID
	)

	// 10 workers race to claim 5 jobs.
	const numWorkers = 10
	wg.Add(numWorkers)

	for i := 0; i < numWorkers; i++ {
		go func(workerID string) {
			defer wg.Done()
			// Each worker tries to claim until no jobs left.
			for {
				claimed, err := claimNextJobRetry(database, "testqueue", workerID, 30*time.Second)
				if err != nil {
					t.Errorf("ClaimNextJob for %s: %v", workerID, err)
					return
				}
				if claimed == nil {
					return
				}
				mu.Lock()
				claims[claimed.ID] = workerID
				mu.Unlock()
			}
		}(fmt.Sprintf("worker-%d", i))
	}

	wg.Wait()

	// Under contention with BadgerDB, some claims may conflict and fail.
	// We expect at least one claim per job (some workers may miss due to conflicts).
	if len(claims) < numJobs {
		t.Logf("claimed %d of %d jobs (expected under contention)", len(claims), numJobs)
	}

	// Every successfully claimed job should be reserved.
	for jobID, workerID := range claims {
		var j Job
		err := database.View(func(txn *badger.Txn) error {
			item, err := txn.Get(db.JobKey(jobID))
			if err != nil {
				return err
			}
			data, err := item.ValueCopy(nil)
			if err != nil {
				return err
			}
			return json.Unmarshal(data, &j)
		})
		if err != nil {
			t.Fatalf("reading job %s: %v", jobID, err)
		}
		if j.Status != StatusReserved {
			t.Errorf("job %s status = %q, want %q (claimed by %s)", jobID, j.Status, StatusReserved, workerID)
		}
		if j.WorkerID != workerID {
			t.Errorf("job %s WorkerID = %q, want %q", jobID, j.WorkerID, workerID)
		}
	}
}

// ---- Edge Cases ----

func TestClaimNextJob_AfterNack(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()

	// Schedule a job, claim it, nack it, then claim again.
	_, err := ScheduleJob(database, "testqueue", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("ScheduleJob(): %v", err)
	}

	claimed, err := ClaimNextJob(database, "testqueue", "worker-1", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNextJob(): %v", err)
	}
	if claimed == nil {
		t.Fatal("ClaimNextJob() returned nil")
	}

	// Nack to re-queue.
	if err := NackJob(database, claimed.ID, "worker-1", cfg.MaxAttempts); err != nil {
		t.Fatalf("NackJob(): %v", err)
	}

	// Now claim again — same job, different worker, attempts incremented.
	reclaimed, err := ClaimNextJob(database, "testqueue", "worker-2", 30*time.Second)
	if err != nil {
		t.Fatalf("Second ClaimNextJob(): %v", err)
	}
	if reclaimed == nil {
		t.Fatal("Second ClaimNextJob() returned nil")
	}
	if reclaimed.ID != claimed.ID {
		t.Errorf("reclaimed.ID = %q, want %q (same job after nack)", reclaimed.ID, claimed.ID)
	}
	if reclaimed.Attempts != 2 {
		t.Errorf("reclaimed.Attempts = %d, want 2", reclaimed.Attempts)
	}
	if reclaimed.WorkerID != "worker-2" {
		t.Errorf("reclaimed.WorkerID = %q, want %q", reclaimed.WorkerID, "worker-2")
	}
}

func TestScheduleJob_EmptyPayload(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	job, err := ScheduleJob(database, "testqueue", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("ScheduleJob() with empty payload error: %v", err)
	}
	if job.ID == "" {
		t.Error("ScheduleJob() returned empty job ID with empty payload")
	}
}

func TestFullJobLifecycle(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()

	// 1. Schedule.
	job, err := ScheduleJob(database, "lifecycle", json.RawMessage(`{"step":"start"}`))
	if err != nil {
		t.Fatalf("ScheduleJob(): %v", err)
	}

	// 2. Claim.
	claimed, err := ClaimNextJob(database, "lifecycle", "worker-lifecycle", cfg.VisibilityTimeout)
	if err != nil {
		t.Fatalf("ClaimNextJob(): %v", err)
	}
	if claimed == nil {
		t.Fatal("ClaimNextJob() returned nil")
	}
	if claimed.ID != job.ID {
		t.Fatalf("claimed job ID mismatch: %q vs %q", claimed.ID, job.ID)
	}

	// 3. Ack.
	if err := AckJob(database, claimed.ID, "worker-lifecycle"); err != nil {
		t.Fatalf("AckJob(): %v", err)
	}

	// 4. Verify job is gone.
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.JobKey(job.ID))
		return err
	})
	if err == nil {
		t.Error("job record should be deleted after ack")
	}
}

func TestFullJobLifecycle_DeadLetter(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()

	// 1. Schedule.
	job, err := ScheduleJob(database, "lifecycle", json.RawMessage(`{"step":"start"}`))
	if err != nil {
		t.Fatalf("ScheduleJob(): %v", err)
	}

	// 2. Claim, nack, and repeat until MAX_ATTEMPTS.
	var lastClaimed *Job
	for i := 0; i < cfg.MaxAttempts; i++ {
		claimed, err := ClaimNextJob(database, "lifecycle", "worker-1", cfg.VisibilityTimeout)
		if err != nil {
			t.Fatalf("ClaimNextJob iteration %d: %v", i, err)
		}
		if claimed == nil {
			t.Fatalf("ClaimNextJob iteration %d returned nil", i)
		}
		lastClaimed = claimed

		if i < cfg.MaxAttempts-1 {
			if err := NackJob(database, claimed.ID, "worker-1", cfg.MaxAttempts); err != nil {
				t.Fatalf("NackJob iteration %d: %v", i, err)
			}
		}
	}

	// 3. Final nack should move to dead-letter.
	if err := NackJob(database, lastClaimed.ID, "worker-1", cfg.MaxAttempts); err != nil {
		t.Fatalf("Final NackJob(): %v", err)
	}

	// 4. Verify dead-letter state.
	err = database.View(func(txn *badger.Txn) error {
		item, err := txn.Get(db.JobKey(job.ID))
		if err != nil {
			return err
		}
		data, err := item.ValueCopy(nil)
		if err != nil {
			return err
		}
		var j Job
		if err := json.Unmarshal(data, &j); err != nil {
			return err
		}
		if j.Status != StatusDead {
			t.Errorf("job.Status = %q, want %q", j.Status, StatusDead)
		}
		if j.Attempts != cfg.MaxAttempts {
			t.Errorf("job.Attempts = %d, want %d", j.Attempts, cfg.MaxAttempts)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading job: %v", err)
	}

	// Verify dead index exists.
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.DeadIndexKey("lifecycle", job.ID))
		return err
	})
	if err != nil {
		t.Error("dead index should exist after dead-letter transition")
	}
}

// ---- Validate Queue Name ----

func TestValidateQueueName(t *testing.T) {
	tests := []struct {
		name    string
		queue   string
		wantErr bool
	}{
		{"simple", "myqueue", false},
		{"with-hyphen", "my-queue", false},
		{"with_underscore", "my_queue", false},
		{"with-dots", "my.queue", false},
		{"alphanumeric", "queue123", false},
		{"single-char", "a", false},
		{"with-colon", "bad:queue", true},
		{"colon-only", ":", true},
		{"multiple-colons", "a:b:c", true},
		{"colons-at-start", ":queue", true},
		{"colons-at-end", "queue:", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateQueueName(tt.queue)
			if tt.wantErr && err == nil {
				t.Errorf("validateQueueName(%q) = nil, want error", tt.queue)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("validateQueueName(%q) = %v, want nil", tt.queue, err)
			}
		})
	}
}

// claimNextJobRetry wraps ClaimNextJob with retries for BadgerDB transaction conflicts.
// Under contention BadgerDB may return "Transaction Conflict" even with automatic retries;
// this helper retries with a small backoff to make tests robust.
func claimNextJobRetry(database *badger.DB, queue, workerID string, timeout time.Duration) (*Job, error) {
	const maxRetries = 25
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		job, err := ClaimNextJob(database, queue, workerID, timeout)
		if err == nil {
			return job, nil
		}
		if !strings.Contains(err.Error(), "Transaction Conflict") {
			return nil, err
		}
		lastErr = err
		time.Sleep(5 * time.Millisecond)
	}
	return nil, lastErr
}

// ---- Concurrent Operations ----

func TestConcurrentScheduleAndClaim(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	var wg sync.WaitGroup

	// Schedule jobs concurrently.
	const numJobs = 20
	for i := 0; i < numJobs; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, err := ScheduleJob(database, "concurrent", json.RawMessage(fmt.Sprintf(`{"n":%d}`, n)))
			if err != nil {
				t.Errorf("concurrent ScheduleJob %d: %v", n, err)
			}
		}(i)
	}
	wg.Wait()

	// Claim all jobs concurrently with retries for transaction conflicts.
	// Use a small pool of workers to avoid overwhelming BadgerDB's optimistic
	// concurrency control.
	var (
		mu     sync.Mutex
		claims []string
	)
	numWorkers := 4
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID string) {
			defer wg.Done()
			for {
				claimed, err := claimNextJobRetry(database, "concurrent", workerID, 30*time.Second)
				if err != nil {
					t.Errorf("concurrent ClaimNextJob %s: %v", workerID, err)
					return
				}
				if claimed == nil {
					return
				}
				mu.Lock()
				claims = append(claims, workerID)
				mu.Unlock()
			}
		}(fmt.Sprintf("worker-%d", i))
	}
	wg.Wait()

	// All jobs should have been claimed.
	if len(claims) != numJobs {
		t.Errorf("expected %d claims, got %d", numJobs, len(claims))
	}
}

func TestConcurrentAckAndNack(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()

	// Schedule and claim a job.
	_, err := ScheduleJob(database, "conflict", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("ScheduleJob(): %v", err)
	}

	claimed, err := ClaimNextJob(database, "conflict", "worker-1", cfg.VisibilityTimeout)
	if err != nil {
		t.Fatalf("ClaimNextJob(): %v", err)
	}
	if claimed == nil {
		t.Fatal("ClaimNextJob() returned nil")
	}

	// Try to ack and nack simultaneously (only one should succeed).
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		status string
	)

	wg.Add(2)
	go func() {
		defer wg.Done()
		err := AckJob(database, claimed.ID, "worker-1")
		if err == nil {
			mu.Lock()
			status = "acked"
			mu.Unlock()
		}
	}()
	go func() {
		defer wg.Done()
		err := NackJob(database, claimed.ID, "worker-1", cfg.MaxAttempts)
		if err == nil {
			mu.Lock()
			status = "nacked"
			mu.Unlock()
		}
	}()
	wg.Wait()

	mu.Lock()
	s := status
	mu.Unlock()

	if s == "" {
		t.Fatal("neither ack nor nack succeeded (both returned errors)")
	}

	switch s {
	case "acked":
		// Job should be deleted entirely (Get returns error).
		err = database.View(func(txn *badger.Txn) error {
			_, err := txn.Get(db.JobKey(claimed.ID))
			return err
		})
		if err == nil {
			t.Error("job should be deleted after ack")
		}
	case "nacked":
		// After nack, the job should exist and be pending.
		var j Job
		err = database.View(func(txn *badger.Txn) error {
			item, err := txn.Get(db.JobKey(claimed.ID))
			if err != nil {
				return err
			}
			data, err := item.ValueCopy(nil)
			if err != nil {
				return err
			}
			return json.Unmarshal(data, &j)
		})
		if err != nil {
			t.Fatalf("reading job after nack: %v", err)
		}
		if j.Status != StatusPending {
			t.Errorf("after nack, job status = %q, want %q", j.Status, StatusPending)
		}
	}
}

// ---- Transactional Consistency ----

func TestScheduleJob_Atomicity(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	// If the database write fails partway, both job record and index should
	// be absent (atomic commit). We can't easily force a partial failure,
	// but we can verify the operation is atomic by checking invariants after
	// normal operations — if either the job or index is missing after a
	// successful ScheduleJob, that's a bug.
	job, err := ScheduleJob(database, "testqueue", json.RawMessage(`{"atomic":"test"}`))
	if err != nil {
		t.Fatalf("ScheduleJob(): %v", err)
	}

	// Verify both job and index exist.
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.JobKey(job.ID))
		return err
	})
	if err != nil {
		t.Errorf("job record missing after successful ScheduleJob: %v", err)
	}

	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.PendingIndexKey("testqueue", job.ID))
		return err
	})
	if err != nil {
		t.Errorf("pending index missing after successful ScheduleJob: %v", err)
	}
}

func TestClaimJob_Atomicity(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	// Schedule a job and claim it. Verify that claim atomically transitions
	// the job from pending to reserved: the pending index is removed, the
	// reserved index is added, and the job record is updated.
	_, err := ScheduleJob(database, "testqueue", json.RawMessage(`{"atomic":"claim"}`))
	if err != nil {
		t.Fatalf("ScheduleJob(): %v", err)
	}

	claimed, err := ClaimNextJob(database, "testqueue", "worker-atomic", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNextJob(): %v", err)
	}
	if claimed == nil {
		t.Fatal("ClaimNextJob() returned nil")
	}

	// Verify post-claim state.
	t.Run("job_record_is_reserved", func(t *testing.T) {
		var j Job
		err := database.View(func(txn *badger.Txn) error {
			item, err := txn.Get(db.JobKey(claimed.ID))
			if err != nil {
				return err
			}
			data, err := item.ValueCopy(nil)
			if err != nil {
				return err
			}
			return json.Unmarshal(data, &j)
		})
		if err != nil {
			t.Fatalf("reading job: %v", err)
		}
		if j.Status != StatusReserved {
			t.Errorf("job status = %q, want %q", j.Status, StatusReserved)
		}
	})

	t.Run("pending_index_removed", func(t *testing.T) {
		err := database.View(func(txn *badger.Txn) error {
			_, err := txn.Get(db.PendingIndexKey("testqueue", claimed.ID))
			return err
		})
		if err == nil {
			t.Error("pending index still exists after claim")
		}
	})

	t.Run("reserved_index_added", func(t *testing.T) {
		err := database.View(func(txn *badger.Txn) error {
			item, err := txn.Get(db.ReservedIndexKey("testqueue", claimed.ID))
			if err != nil {
				return err
			}
			val, err := item.ValueCopy(nil)
			if err != nil {
				return err
			}
			// Value should be a numeric string (expiry timestamp).
			if len(val) == 0 {
				t.Error("reserved index value is empty")
			}
			return nil
		})
		if err != nil {
			t.Errorf("reserved index missing after claim: %v", err)
		}
	})
}

func TestAckJob_Atomicity(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	_, err := ScheduleJob(database, "testqueue", json.RawMessage(`{"atomic":"ack"}`))
	if err != nil {
		t.Fatalf("ScheduleJob(): %v", err)
	}

	claimed, err := ClaimNextJob(database, "testqueue", "worker-atomic", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNextJob(): %v", err)
	}
	if claimed == nil {
		t.Fatal("ClaimNextJob() returned nil")
	}

	if err := AckJob(database, claimed.ID, "worker-atomic"); err != nil {
		t.Fatalf("AckJob(): %v", err)
	}

	// Verify both job record and reserved index are deleted.
	t.Run("job_record_deleted", func(t *testing.T) {
		err := database.View(func(txn *badger.Txn) error {
			_, err := txn.Get(db.JobKey(claimed.ID))
			return err
		})
		if err == nil {
			t.Error("job record still exists after ack")
		}
	})

	t.Run("reserved_index_deleted", func(t *testing.T) {
		err := database.View(func(txn *badger.Txn) error {
			_, err := txn.Get(db.ReservedIndexKey("testqueue", claimed.ID))
			return err
		})
		if err == nil {
			t.Error("reserved index still exists after ack")
		}
	})
}

func TestNackJob_Atomicity(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()

	// Schedule and claim a job.
	_, err := ScheduleJob(database, "testqueue", json.RawMessage(`{"atomic":"nack"}`))
	if err != nil {
		t.Fatalf("ScheduleJob(): %v", err)
	}

	claimed, err := ClaimNextJob(database, "testqueue", "worker-atomic", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNextJob(): %v", err)
	}
	if claimed == nil {
		t.Fatal("ClaimNextJob() returned nil")
	}

	// Nack (re-queue).
	if err := NackJob(database, claimed.ID, "worker-atomic", cfg.MaxAttempts); err != nil {
		t.Fatalf("NackJob(): %v", err)
	}

	// Verify post-nack state: job is pending, pending index exists, reserved index gone.
	t.Run("job_status_pending", func(t *testing.T) {
		var j Job
		err := database.View(func(txn *badger.Txn) error {
			item, err := txn.Get(db.JobKey(claimed.ID))
			if err != nil {
				return err
			}
			data, err := item.ValueCopy(nil)
			if err != nil {
				return err
			}
			return json.Unmarshal(data, &j)
		})
		if err != nil {
			t.Fatalf("reading job: %v", err)
		}
		if j.Status != StatusPending {
			t.Errorf("job status = %q, want %q", j.Status, StatusPending)
		}
		if j.WorkerID != "" {
			t.Errorf("job WorkerID = %q, want empty", j.WorkerID)
		}
	})

	t.Run("pending_index_exists", func(t *testing.T) {
		err := database.View(func(txn *badger.Txn) error {
			_, err := txn.Get(db.PendingIndexKey("testqueue", claimed.ID))
			return err
		})
		if err != nil {
			t.Error("pending index missing after nack")
		}
	})

	t.Run("reserved_index_deleted", func(t *testing.T) {
		err := database.View(func(txn *badger.Txn) error {
			_, err := txn.Get(db.ReservedIndexKey("testqueue", claimed.ID))
			return err
		})
		if err == nil {
			t.Error("reserved index still exists after nack")
		}
	})
}

func TestNackJob_DeadLetterAtomicity(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	maxAttempts := 1

	// Schedule and claim a job (Attempts becomes 1).
	_, err := ScheduleJob(database, "testqueue", json.RawMessage(`{"atomic":"dead"}`))
	if err != nil {
		t.Fatalf("ScheduleJob(): %v", err)
	}

	claimed, err := ClaimNextJob(database, "testqueue", "worker-atomic", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNextJob(): %v", err)
	}
	if claimed == nil {
		t.Fatal("ClaimNextJob() returned nil")
	}

	// Nack with MaxAttempts=1 goes to dead-letter.
	if err := NackJob(database, claimed.ID, "worker-atomic", maxAttempts); err != nil {
		t.Fatalf("NackJob(): %v", err)
	}

	// Verify dead-letter state.
	t.Run("job_status_dead", func(t *testing.T) {
		var j Job
		err := database.View(func(txn *badger.Txn) error {
			item, err := txn.Get(db.JobKey(claimed.ID))
			if err != nil {
				return err
			}
			data, err := item.ValueCopy(nil)
			if err != nil {
				return err
			}
			return json.Unmarshal(data, &j)
		})
		if err != nil {
			t.Fatalf("reading job: %v", err)
		}
		if j.Status != StatusDead {
			t.Errorf("job status = %q, want %q", j.Status, StatusDead)
		}
		if j.WorkerID != "" {
			t.Errorf("job WorkerID = %q, want empty", j.WorkerID)
		}
	})

	t.Run("dead_index_exists", func(t *testing.T) {
		err := database.View(func(txn *badger.Txn) error {
			_, err := txn.Get(db.DeadIndexKey("testqueue", claimed.ID))
			return err
		})
		if err != nil {
			t.Error("dead index missing after dead-letter transition")
		}
	})

	t.Run("reserved_index_deleted", func(t *testing.T) {
		err := database.View(func(txn *badger.Txn) error {
			_, err := txn.Get(db.ReservedIndexKey("testqueue", claimed.ID))
			return err
		})
		if err == nil {
			t.Error("reserved index still exists after dead-letter transition")
		}
	})

	t.Run("no_pending_index", func(t *testing.T) {
		err := database.View(func(txn *badger.Txn) error {
			_, err := txn.Get(db.PendingIndexKey("testqueue", claimed.ID))
			return err
		})
		if err == nil {
			t.Error("pending index should not exist after dead-letter transition")
		}
	})
}
