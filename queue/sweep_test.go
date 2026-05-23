//nolint:wrapcheck // test assertions pass through external errors; wrapping is noise here.
package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	badger "github.com/dgraph-io/badger/v4"

	"github.com/mkoziy/http-queue/config"
	"github.com/mkoziy/http-queue/db"
	"github.com/mkoziy/http-queue/token"
)

// sweepTestConfig returns a config with short timeouts suitable for sweep testing.
func sweepTestConfig() *config.Config {
	return &config.Config{
		AdminUser:         "admin",
		AdminPass:         "pass",
		VisibilityTimeout: 30 * time.Second,
		WorkerExpiry:      5 * time.Minute,
		SweepInterval:     1 * time.Hour, // prevent auto-triggering during tests
		MaxAttempts:       3,
		LastSeenDebounce:  1 * time.Hour, // prevent auto-flush during tests
	}
}

// ---- Database helpers for setting up test state ----

// setWorkerLastSeen directly modifies a worker's LastSeen timestamp in the database.
func setWorkerLastSeen(t *testing.T, database *badger.DB, workerID string, lastSeen time.Time) {
	t.Helper()
	err := database.Update(func(txn *badger.Txn) error {
		item, err := txn.Get(db.WorkerKey(workerID))
		if err != nil {
			return fmt.Errorf("get worker %s: %w", workerID, err)
		}
		data, err := item.ValueCopy(nil)
		if err != nil {
			return fmt.Errorf("value copy: %w", err)
		}
		var w Worker
		if err := json.Unmarshal(data, &w); err != nil {
			return fmt.Errorf("unmarshal worker: %w", err)
		}
		w.LastSeen = lastSeen
		updated, err := json.Marshal(w)
		if err != nil {
			return fmt.Errorf("marshal worker: %w", err)
		}
		return txn.Set(db.WorkerKey(workerID), updated)
	})
	if err != nil {
		t.Fatalf("setWorkerLastSeen: %v", err)
	}
}

// setReservedExpiry directly modifies the expiry timestamp on a reserved index key.
func setReservedExpiry(t *testing.T, database *badger.DB, queue, jobID string, expiry time.Time) {
	t.Helper()
	err := database.Update(func(txn *badger.Txn) error {
		return txn.Set(db.ReservedIndexKey(queue, jobID), []byte(fmt.Sprintf("%d", expiry.Unix())))
	})
	if err != nil {
		t.Fatalf("setReservedExpiry: %v", err)
	}
}

// setJobFields directly modifies specific fields of a job record.
func setJobFields(t *testing.T, database *badger.DB, jobID string, opts struct {
	Status   JobStatus
	WorkerID string
	Attempts int
	Queue    string
}) {
	t.Helper()
	err := database.Update(func(txn *badger.Txn) error {
		item, err := txn.Get(db.JobKey(jobID))
		if err != nil {
			return fmt.Errorf("get job %s: %w", jobID, err)
		}
		data, err := item.ValueCopy(nil)
		if err != nil {
			return fmt.Errorf("value copy: %w", err)
		}
		var j Job
		if err := json.Unmarshal(data, &j); err != nil {
			return fmt.Errorf("unmarshal job: %w", err)
		}
		if opts.Status != "" {
			j.Status = opts.Status
		}
		j.WorkerID = opts.WorkerID
		if opts.Attempts > 0 || opts.Status == StatusDead {
			j.Attempts = opts.Attempts
		}
		if opts.Queue != "" {
			j.Queue = opts.Queue
		}
		updated, err := json.Marshal(j)
		if err != nil {
			return fmt.Errorf("marshal job: %w", err)
		}
		return txn.Set(db.JobKey(jobID), updated)
	})
	if err != nil {
		t.Fatalf("setJobFields: %v", err)
	}
}

// deleteKey removes a single key from the database.
func deleteKey(t *testing.T, database *badger.DB, key []byte) {
	t.Helper()
	err := database.Update(func(txn *badger.Txn) error {
		return txn.Delete(key)
	})
	if err != nil {
		t.Fatalf("deleteKey(%q): %v", string(key), err)
	}
}

// ---- NewSweeper ----

func TestNewSweeper(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := sweepTestConfig()
	s := NewSweeper(database, cfg)
	if s == nil {
		t.Fatal("NewSweeper() returned nil")
	}
	if s.db != database {
		t.Error("NewSweeper() db field mismatch")
	}
	if s.cfg != cfg {
		t.Error("NewSweeper() cfg field mismatch")
	}
}

// ---- Expired Reservations ----

func TestSweep_ExpiredReservation_Requeued(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := sweepTestConfig()

	// Schedule a job.
	_, err := ScheduleJob(database, "testqueue", json.RawMessage(`{"n":1}`))
	if err != nil {
		t.Fatalf("ScheduleJob(): %v", err)
	}

	// Claim it (creates reserved index with a future expiry).
	claimed, err := ClaimNextJob(database, "testqueue", "worker-1", cfg.VisibilityTimeout)
	if err != nil {
		t.Fatalf("ClaimNextJob(): %v", err)
	}
	if claimed == nil {
		t.Fatal("ClaimNextJob() returned nil")
	}

	// Manually set the reserved index expiry to the past (1 hour ago).
	setReservedExpiry(t, database, "testqueue", claimed.ID, time.Now().UTC().Add(-1*time.Hour))

	// Run sweep.
	sweeper := NewSweeper(database, cfg)
	sweeper.expireReservations()

	// Verify the job is back to pending.
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
			t.Errorf("job status = %q, want %q", j.Status, StatusPending)
		}
		if j.WorkerID != "" {
			t.Errorf("job WorkerID = %q, want empty", j.WorkerID)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading job: %v", err)
	}

	// Verify pending index exists.
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.PendingIndexKey("testqueue", claimed.ID))
		return err
	})
	if err != nil {
		t.Error("pending index missing after expired reservation re-queue")
	}

	// Verify reserved index is gone.
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.ReservedIndexKey("testqueue", claimed.ID))
		return err
	})
	if err == nil {
		t.Error("reserved index still exists after expired reservation re-queue")
	}
}

func TestSweep_ExpiredReservation_DeadLetter(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := sweepTestConfig()

	// Schedule a job.
	_, err := ScheduleJob(database, "testqueue", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("ScheduleJob(): %v", err)
	}

	// Claim it (Attempts becomes 1).
	claimed, err := ClaimNextJob(database, "testqueue", "worker-1", cfg.VisibilityTimeout)
	if err != nil {
		t.Fatalf("ClaimNextJob(): %v", err)
	}
	if claimed == nil {
		t.Fatal("ClaimNextJob() returned nil")
	}

	// Manually set Attempts to MaxAttempts to simulate exhausted retries,
	// and keep status=reserved.
	setJobFields(t, database, claimed.ID, struct {
		Status   JobStatus
		WorkerID string
		Attempts int
		Queue    string
	}{
		Status:   StatusReserved,
		WorkerID: "worker-1",
		Attempts: cfg.MaxAttempts,
	})

	// Set reserved index expiry to the past.
	setReservedExpiry(t, database, "testqueue", claimed.ID, time.Now().UTC().Add(-1*time.Hour))

	// Run sweep.
	sweeper := NewSweeper(database, cfg)
	sweeper.expireReservations()

	// Verify job is dead.
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
			t.Errorf("job status = %q, want %q", j.Status, StatusDead)
		}
		if j.WorkerID != "" {
			t.Errorf("job WorkerID = %q, want empty after dead-letter", j.WorkerID)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading job: %v", err)
	}

	// Verify dead index exists.
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.DeadIndexKey("testqueue", claimed.ID))
		return err
	})
	if err != nil {
		t.Error("dead index missing after expired reservation dead-letter")
	}

	// Verify reserved index is gone.
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.ReservedIndexKey("testqueue", claimed.ID))
		return err
	})
	if err == nil {
		t.Error("reserved index still exists after dead-letter")
	}
}

func TestSweep_NonExpiredReservation_Untouched(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := sweepTestConfig()

	// Schedule a job.
	_, err := ScheduleJob(database, "testqueue", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("ScheduleJob(): %v", err)
	}

	// Claim it (creates reserved index with future expiry).
	claimed, err := ClaimNextJob(database, "testqueue", "worker-1", cfg.VisibilityTimeout)
	if err != nil {
		t.Fatalf("ClaimNextJob(): %v", err)
	}
	if claimed == nil {
		t.Fatal("ClaimNextJob() returned nil")
	}

	// Run sweep immediately (before visibility timeout expires).
	sweeper := NewSweeper(database, cfg)
	sweeper.expireReservations()

	// Verify the job is still reserved (not re-queued).
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
			t.Errorf("job status = %q, want %q (should be untouched)", j.Status, StatusReserved)
		}
		if j.WorkerID != "worker-1" {
			t.Errorf("job WorkerID = %q, want %q", j.WorkerID, "worker-1")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading job: %v", err)
	}

	// Verify reserved index still exists.
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.ReservedIndexKey("testqueue", claimed.ID))
		return err
	})
	if err != nil {
		t.Error("reserved index should still exist for non-expired reservation")
	}
}

// ---- Expired Workers ----

func TestSweep_ExpiredWorker_Deregistered(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := sweepTestConfig()

	// Register a worker.
	id, plainToken, err := RegisterWorker(database, cfg)
	if err != nil {
		t.Fatalf("RegisterWorker(): %v", err)
	}

	// Set its lastSeen to 1 hour ago (beyond WorkerExpiry of 5 minutes).
	setWorkerLastSeen(t, database, id, time.Now().UTC().Add(-1*time.Hour))
	// Clear in-memory cache to reflect that the worker stopped sending heartbeats.
	workerLastSeen.Delete(id)
	workerLastSeen.Delete("flush:" + id)

	// Run sweep.
	sweeper := NewSweeper(database, cfg)
	sweeper.expireWorkers()

	// Verify worker record is gone.
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.WorkerKey(id))
		return err
	})
	if err == nil {
		t.Error("worker record still exists after expired worker sweep")
	}

	// Verify token index is gone.
	hashedToken := token.Hash(plainToken)
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.WorkerTokenKey(hashedToken))
		return err
	})
	if err == nil {
		t.Error("worker token index still exists after expired worker sweep")
	}

	// Verify in-memory cache was cleaned up.
	_, ok := workerLastSeen.Load(id)
	if ok {
		t.Error("worker still in in-memory last-seen cache after expired worker sweep")
	}
}

func TestSweep_NonExpiredWorker_Untouched(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := sweepTestConfig()

	// Register a worker (lastSeen is set to now).
	id, plainToken, err := RegisterWorker(database, cfg)
	if err != nil {
		t.Fatalf("RegisterWorker(): %v", err)
	}

	// Run sweep immediately (worker hasn't expired yet).
	sweeper := NewSweeper(database, cfg)
	sweeper.expireWorkers()

	// Verify worker record still exists.
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.WorkerKey(id))
		return err
	})
	if err != nil {
		t.Error("worker record should still exist for non-expired worker")
	}

	// Verify token index still exists.
	hashedToken := token.Hash(plainToken)
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.WorkerTokenKey(hashedToken))
		return err
	})
	if err != nil {
		t.Error("worker token index should still exist for non-expired worker")
	}
}

func TestSweep_ExpiredWorker_RequeuesReservedJobs(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := sweepTestConfig()

	// Register a worker.
	id, _, err := RegisterWorker(database, cfg)
	if err != nil {
		t.Fatalf("RegisterWorker(): %v", err)
	}

	// Schedule and claim a job with this worker.
	_, err = ScheduleJob(database, "testqueue", json.RawMessage(`{"hello":"world"}`))
	if err != nil {
		t.Fatalf("ScheduleJob(): %v", err)
	}

	claimed, err := ClaimNextJob(database, "testqueue", id, cfg.VisibilityTimeout)
	if err != nil {
		t.Fatalf("ClaimNextJob(): %v", err)
	}
	if claimed == nil {
		t.Fatal("ClaimNextJob() returned nil")
	}

	// Set worker lastSeen to the past to make it expired.
	setWorkerLastSeen(t, database, id, time.Now().UTC().Add(-1*time.Hour))
	// Clear in-memory cache to reflect that the worker stopped sending heartbeats.
	workerLastSeen.Delete(id)
	workerLastSeen.Delete("flush:" + id)

	// Run full sweep (expireWorkers will deregister the worker, which requeues
	// its reserved jobs).
	sweeper := NewSweeper(database, cfg)
	sweeper.expireWorkers()

	// Verify worker is gone.
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.WorkerKey(id))
		return err
	})
	if err == nil {
		t.Error("worker record should be deleted after expiry")
	}

	// Verify the job is back to pending.
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
			t.Errorf("job status = %q, want %q after worker expiry", j.Status, StatusPending)
		}
		if j.WorkerID != "" {
			t.Errorf("job WorkerID = %q, want empty after worker expiry", j.WorkerID)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading job: %v", err)
	}

	// Verify pending index exists.
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.PendingIndexKey("testqueue", claimed.ID))
		return err
	})
	if err != nil {
		t.Error("pending index missing after worker expiry (job should be re-queued)")
	}

	// Verify reserved index is gone.
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.ReservedIndexKey("testqueue", claimed.ID))
		return err
	})
	if err == nil {
		t.Error("reserved index should be gone after worker expiry")
	}
}

// ---- Reconciliation ----

func TestSweep_Reconcile_OrphanedReservedJob_Requeued(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := sweepTestConfig()

	// Schedule a job to create a job record.
	scheduled, err := ScheduleJob(database, "testqueue", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("ScheduleJob(): %v", err)
	}

	// Manually set the job to status=reserved with a worker, but don't create
	// a reserved index — this simulates an orphaned reserved job (crash scenario).
	setJobFields(t, database, scheduled.ID, struct {
		Status   JobStatus
		WorkerID string
		Attempts int
		Queue    string
	}{
		Status:   StatusReserved,
		WorkerID: "orphan-worker",
		Attempts: 1,
	})

	// Delete the pending index to make it truly orphaned.
	deleteKey(t, database, db.PendingIndexKey("testqueue", scheduled.ID))

	// Run reconciliation.
	sweeper := NewSweeper(database, cfg)
	sweeper.reconcile()

	// Verify the job is back to pending.
	err = database.View(func(txn *badger.Txn) error {
		item, err := txn.Get(db.JobKey(scheduled.ID))
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
			t.Errorf("job status = %q, want %q after reconciliation", j.Status, StatusPending)
		}
		if j.WorkerID != "" {
			t.Errorf("job WorkerID = %q, want empty after reconciliation", j.WorkerID)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading job: %v", err)
	}

	// Verify pending index was created.
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.PendingIndexKey("testqueue", scheduled.ID))
		return err
	})
	if err != nil {
		t.Error("pending index should exist after reconciliation of orphaned reserved job")
	}
}

func TestSweep_Reconcile_PhantomPendingIndex_Removed(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := sweepTestConfig()

	// Write a phantom pending index key with no matching job record.
	phantomULID := "01AR00000000000000000000"
	err := database.Update(func(txn *badger.Txn) error {
		return txn.Set(db.PendingIndexKey("testqueue", phantomULID), nil)
	})
	if err != nil {
		t.Fatalf("writing phantom pending index: %v", err)
	}

	// Run reconciliation.
	sweeper := NewSweeper(database, cfg)
	sweeper.reconcile()

	// Verify the phantom pending index is gone.
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.PendingIndexKey("testqueue", phantomULID))
		return err
	})
	if err == nil {
		t.Error("phantom pending index should have been removed by reconciliation")
	}
}

func TestSweep_Reconcile_MissingPendingIndex_Restored(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := sweepTestConfig()

	// Schedule a job (creates both job record and pending index).
	scheduled, err := ScheduleJob(database, "testqueue", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("ScheduleJob(): %v", err)
	}

	// Delete the pending index to simulate a crash where only the job record
	// was persisted but the index was lost.
	deleteKey(t, database, db.PendingIndexKey("testqueue", scheduled.ID))

	// Run reconciliation.
	sweeper := NewSweeper(database, cfg)
	sweeper.reconcile()

	// Verify the pending index was re-created.
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.PendingIndexKey("testqueue", scheduled.ID))
		return err
	})
	if err != nil {
		t.Error("missing pending index should have been restored by reconciliation")
	}

	// Verify the job record is still intact.
	err = database.View(func(txn *badger.Txn) error {
		item, err := txn.Get(db.JobKey(scheduled.ID))
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
			t.Errorf("job status = %q, want %q", j.Status, StatusPending)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading job: %v", err)
	}
}

// ---- Full Sweep ----

func TestSweep_FullSweep_CleansExpiredState(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := sweepTestConfig()

	// --- Set up expired worker with a reserved job ---

	workerID, _, err := RegisterWorker(database, cfg)
	if err != nil {
		t.Fatalf("RegisterWorker(): %v", err)
	}

	_, err = ScheduleJob(database, "testqueue", json.RawMessage(`{"owner":"expired-worker"}`))
	if err != nil {
		t.Fatalf("ScheduleJob(): %v", err)
	}

	claimed, err := ClaimNextJob(database, "testqueue", workerID, cfg.VisibilityTimeout)
	if err != nil {
		t.Fatalf("ClaimNextJob(): %v", err)
	}
	if claimed == nil {
		t.Fatal("ClaimNextJob() returned nil")
	}

	// Expire the worker by setting its lastSeen to the past.
	setWorkerLastSeen(t, database, workerID, time.Now().UTC().Add(-1*time.Hour))
	// Clear in-memory cache to reflect that the worker stopped sending heartbeats.
	workerLastSeen.Delete(workerID)
	workerLastSeen.Delete("flush:" + workerID)

	// --- Set up expired reservation (for a different queue) ---
	_, err = ScheduleJob(database, "otherqueue", json.RawMessage(`{"expired":"reservation"}`))
	if err != nil {
		t.Fatalf("ScheduleJob(): %v", err)
	}

	otherClaimed, err := ClaimNextJob(database, "otherqueue", "other-worker", cfg.VisibilityTimeout)
	if err != nil {
		t.Fatalf("ClaimNextJob(): %v", err)
	}
	if otherClaimed == nil {
		t.Fatal("ClaimNextJob() returned nil")
	}

	// Expire just the reservation by setting a past expiry.
	setReservedExpiry(t, database, "otherqueue", otherClaimed.ID, time.Now().UTC().Add(-1*time.Hour))

	// --- Run full sweep ---
	sweeper := NewSweeper(database, cfg)
	sweeper.sweep()

	// --- Verify expired worker was cleaned up ---
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.WorkerKey(workerID))
		return err
	})
	if err == nil {
		t.Error("expired worker record should be deleted after full sweep")
	}

	// The expired worker's job should be back to pending.
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
			t.Errorf("expired worker's job status = %q, want %q", j.Status, StatusPending)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading job from expired worker: %v", err)
	}

	// --- Verify expired reservation was re-queued ---
	err = database.View(func(txn *badger.Txn) error {
		item, err := txn.Get(db.JobKey(otherClaimed.ID))
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
			t.Errorf("expired reservation job status = %q, want %q", j.Status, StatusPending)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading expired reservation job: %v", err)
	}

	// Verify reserved index is gone for the expired reservation.
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.ReservedIndexKey("otherqueue", otherClaimed.ID))
		return err
	})
	if err == nil {
		t.Error("reserved index should be gone for expired reservation after full sweep")
	}
}

// ---- Sweeper Start/Stop ----

func TestSweeper_Start_StopsOnContextCancel(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := sweepTestConfig()
	cfg.SweepInterval = 10 * time.Millisecond // fast ticker for the test

	sweeper := NewSweeper(database, cfg)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		sweeper.Start(ctx)
		close(done)
	}()

	// Let it run for a couple ticks.
	time.Sleep(50 * time.Millisecond)

	// Cancel the context.
	cancel()

	// Wait for Start to return (with timeout).
	select {
	case <-done:
		// Success.
	case <-time.After(5 * time.Second):
		t.Fatal("Sweeper.Start() did not return within 5 seconds after context cancel")
	}
}

func TestSweeper_Start_RunsInitialSweep(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := sweepTestConfig()
	cfg.SweepInterval = 1 * time.Hour // very long ticker so only the initial sweep runs

	// Set up an expired reservation.
	_, err := ScheduleJob(database, "testqueue", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("ScheduleJob(): %v", err)
	}

	claimed, err := ClaimNextJob(database, "testqueue", "worker-1", cfg.VisibilityTimeout)
	if err != nil {
		t.Fatalf("ClaimNextJob(): %v", err)
	}
	if claimed == nil {
		t.Fatal("ClaimNextJob() returned nil")
	}

	setReservedExpiry(t, database, "testqueue", claimed.ID, time.Now().UTC().Add(-1*time.Hour))

	// Start the sweeper (should run an initial sweep immediately).
	sweeper := NewSweeper(database, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		sweeper.Start(ctx)
		close(done)
	}()

	// Give it time to run the initial sweep.
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done

	// Verify the initial sweep re-queued the expired reservation.
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
			t.Errorf("job status = %q, want %q (initial sweep should have re-queued)", j.Status, StatusPending)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading job: %v", err)
	}
}

// ---- Sweep + Claim Race ----

func TestSweep_ClaimRace(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := sweepTestConfig()
	cfg.VisibilityTimeout = 5 * time.Second // short timeout

	// Schedule one job.
	_, err := ScheduleJob(database, "racequeue", json.RawMessage(`{"race":"test"}`))
	if err != nil {
		t.Fatalf("ScheduleJob(): %v", err)
	}

	// Claim the job.
	claimed, err := ClaimNextJob(database, "racequeue", "worker-race", cfg.VisibilityTimeout)
	if err != nil {
		t.Fatalf("ClaimNextJob(): %v", err)
	}
	if claimed == nil {
		t.Fatal("ClaimNextJob() returned nil")
	}

	// Set the reserved index expiry to right now (borderline expired).
	setReservedExpiry(t, database, "racequeue", claimed.ID, time.Now().UTC())

	sweeper := NewSweeper(database, cfg)

	var wg sync.WaitGroup
	wg.Add(2)

	// Goroutine 1: sweep expires the reservation.
	go func() {
		defer wg.Done()
		sweeper.expireReservations()
	}()

	// Goroutine 2: worker tries to ack the job at the same time.
	go func() {
		defer wg.Done()
		// Either the ack or the sweep will succeed. The other should fail
		// due to transaction conflict. That's expected.
		_ = AckJob(database, claimed.ID, "worker-race")
	}()

	wg.Wait()

	// The final state should be consistent:
	// - Either the job is acked (completely gone) or re-queued (status=pending).
	// - There should be no orphaned reserved state.

	var j Job
	err = database.View(func(txn *badger.Txn) error {
		item, err := txn.Get(db.JobKey(claimed.ID))
		if err != nil {
			// Job was acked — this is valid.
			return err
		}
		data, err := item.ValueCopy(nil)
		if err != nil {
			return err
		}
		return json.Unmarshal(data, &j)
	})

	if err != nil {
		// Job was deleted by ack — that's one valid outcome.
		t.Log("race result: job was acked (deleted)")
	} else {
		// Job still exists — must be pending (sweep won).
		if j.Status != StatusPending {
			t.Errorf("after race, job status = %q, want %q", j.Status, StatusPending)
		}
		if j.WorkerID != "" {
			t.Errorf("after race, job WorkerID = %q, want empty", j.WorkerID)
		}
		t.Log("race result: job was re-queued by sweep")

		// Verify no reserved index exists.
		err = database.View(func(txn *badger.Txn) error {
			_, err := txn.Get(db.ReservedIndexKey("racequeue", claimed.ID))
			return err
		})
		if err == nil {
			t.Error("reserved index should not exist after race")
		}
	}
}
