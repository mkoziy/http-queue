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

// verifyJobInvariants checks that a job's backing indexes and fields are
// consistent with its status. This is a post-maintenance invariant check.
func verifyJobInvariants(t *testing.T, database *badger.DB, jobID, queue string) {
	t.Helper()

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
		t.Fatalf("read job %s for invariant check: %v", jobID, err)
	}

	switch j.Status {
	case StatusPending:
		// Must have pending index.
		if err := database.View(func(txn *badger.Txn) error {
			_, err := txn.Get(db.PendingIndexKey(queue, jobID))
			return err
		}); err != nil {
			t.Errorf("invariant: pending job %s missing pending index", jobID)
		}
		// Must NOT have reserved index.
		if err := database.View(func(txn *badger.Txn) error {
			_, err := txn.Get(db.ReservedIndexKey(queue, jobID))
			return err
		}); err == nil {
			t.Errorf("invariant: pending job %s has stale reserved index", jobID)
		}
		// Must NOT have WorkerID set.
		if j.WorkerID != "" {
			t.Errorf("invariant: pending job %s has WorkerID=%q, want empty", jobID, j.WorkerID)
		}

	case StatusReserved:
		// Must have reserved index.
		if err := database.View(func(txn *badger.Txn) error {
			_, err := txn.Get(db.ReservedIndexKey(queue, jobID))
			return err
		}); err != nil {
			t.Errorf("invariant: reserved job %s missing reserved index", jobID)
		}
		// Must NOT have pending index.
		if err := database.View(func(txn *badger.Txn) error {
			_, err := txn.Get(db.PendingIndexKey(queue, jobID))
			return err
		}); err == nil {
			t.Errorf("invariant: reserved job %s has stale pending index", jobID)
		}
		// Must have WorkerID set.
		if j.WorkerID == "" {
			t.Errorf("invariant: reserved job %s has empty WorkerID", jobID)
		}

	case StatusDead:
		// Must have dead index.
		if err := database.View(func(txn *badger.Txn) error {
			_, err := txn.Get(db.DeadIndexKey(queue, jobID))
			return err
		}); err != nil {
			t.Errorf("invariant: dead job %s missing dead index", jobID)
		}
		// Must NOT have reserved index.
		if err := database.View(func(txn *badger.Txn) error {
			_, err := txn.Get(db.ReservedIndexKey(queue, jobID))
			return err
		}); err == nil {
			t.Errorf("invariant: dead job %s has stale reserved index", jobID)
		}
		// Must NOT have WorkerID set.
		if j.WorkerID != "" {
			t.Errorf("invariant: dead job %s has WorkerID=%q, want empty", jobID, j.WorkerID)
		}

	default:
		t.Errorf("invariant: job %s has unknown status %q", jobID, j.Status)
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

func TestSweep_ManyExpiredReservations_RequeuedAndDeadLettered(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := sweepTestConfig()

	// Register a single worker to own all reserved jobs.
	workerID, _, err := RegisterWorker(database, cfg)
	if err != nil {
		t.Fatalf("RegisterWorker(): %v", err)
	}

	// Create enough jobs to span multiple maintenance batches.
	numJobs := maintenanceBatchSize + 10

	// Split: first numRequeue jobs will be re-queued (attempts < MaxAttempts),
	// remaining numDeadLetter jobs will be dead-lettered (attempts == MaxAttempts).
	numDeadLetter := 5
	numRequeue := numJobs - numDeadLetter

	claimedRequeue := make([]*Job, 0, numRequeue)
	claimedDead := make([]*Job, 0, numDeadLetter)

	for i := 0; i < numJobs; i++ {
		_, err := ScheduleJob(database, "batchqueue", json.RawMessage(`{"n":`+fmt.Sprintf("%d", i)+`}`))
		if err != nil {
			t.Fatalf("ScheduleJob %d: %v", i, err)
		}

		claimed, err := ClaimNextJob(database, "batchqueue", workerID, cfg.VisibilityTimeout)
		if err != nil {
			t.Fatalf("ClaimNextJob %d: %v", i, err)
		}
		if claimed == nil {
			t.Fatalf("ClaimNextJob %d returned nil", i)
		}

		if i < numRequeue {
			claimedRequeue = append(claimedRequeue, claimed)
		} else {
			claimedDead = append(claimedDead, claimed)
		}
	}

	// Set the dead-letter batch jobs to MaxAttempts attempts.
	for _, j := range claimedDead {
		setJobFields(t, database, j.ID, struct {
			Status   JobStatus
			WorkerID string
			Attempts int
			Queue    string
		}{
			Status:   StatusReserved,
			WorkerID: workerID,
			Attempts: cfg.MaxAttempts,
		})
	}

	// Expire all reservations by setting reserved index expiry to the past.
	for _, j := range claimedRequeue {
		setReservedExpiry(t, database, "batchqueue", j.ID, time.Now().UTC().Add(-1*time.Hour))
	}
	for _, j := range claimedDead {
		setReservedExpiry(t, database, "batchqueue", j.ID, time.Now().UTC().Add(-1*time.Hour))
	}

	// Run sweep.
	sweeper := NewSweeper(database, cfg)
	sweeper.expireReservations()

	// --- Verify re-queued jobs ---
	for _, j := range claimedRequeue {
		err := database.View(func(txn *badger.Txn) error {
			item, err := txn.Get(db.JobKey(j.ID))
			if err != nil {
				return err
			}
			data, err := item.ValueCopy(nil)
			if err != nil {
				return err
			}
			var job Job
			if err := json.Unmarshal(data, &job); err != nil {
				return err
			}
			if job.Status != StatusPending {
				t.Errorf("requeue job %s status = %q, want %q", j.ID, job.Status, StatusPending)
			}
			if job.WorkerID != "" {
				t.Errorf("requeue job %s WorkerID = %q, want empty", j.ID, job.WorkerID)
			}
			return nil
		})
		if err != nil {
			t.Errorf("reading re-queued job %s: %v", j.ID, err)
		}

		// Verify pending index exists.
		err = database.View(func(txn *badger.Txn) error {
			_, err := txn.Get(db.PendingIndexKey("batchqueue", j.ID))
			return err
		})
		if err != nil {
			t.Errorf("requeue job %s: missing pending index", j.ID)
		}

		// Verify reserved index is gone.
		err = database.View(func(txn *badger.Txn) error {
			_, err := txn.Get(db.ReservedIndexKey("batchqueue", j.ID))
			return err
		})
		if err == nil {
			t.Errorf("requeue job %s: reserved index still exists", j.ID)
		}
	}

	// --- Verify dead-lettered jobs ---
	for _, j := range claimedDead {
		err := database.View(func(txn *badger.Txn) error {
			item, err := txn.Get(db.JobKey(j.ID))
			if err != nil {
				return err
			}
			data, err := item.ValueCopy(nil)
			if err != nil {
				return err
			}
			var job Job
			if err := json.Unmarshal(data, &job); err != nil {
				return err
			}
			if job.Status != StatusDead {
				t.Errorf("dead-letter job %s status = %q, want %q", j.ID, job.Status, StatusDead)
			}
			if job.WorkerID != "" {
				t.Errorf("dead-letter job %s WorkerID = %q, want empty", j.ID, job.WorkerID)
			}
			return nil
		})
		if err != nil {
			t.Errorf("reading dead-letter job %s: %v", j.ID, err)
		}

		// Verify dead index exists.
		err = database.View(func(txn *badger.Txn) error {
			_, err := txn.Get(db.DeadIndexKey("batchqueue", j.ID))
			return err
		})
		if err != nil {
			t.Errorf("dead-letter job %s: missing dead index", j.ID)
		}

		// Verify reserved index is gone.
		err = database.View(func(txn *badger.Txn) error {
			_, err := txn.Get(db.ReservedIndexKey("batchqueue", j.ID))
			return err
		})
		if err == nil {
			t.Errorf("dead-letter job %s: reserved index still exists", j.ID)
		}
	}
}

func TestSweep_ExpiredReservation_SkipsUnparseableAndProcessesValid(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := sweepTestConfig()

	// Job 1: valid expired reservation (should be re-queued).
	_, err := ScheduleJob(database, "queue-a", json.RawMessage(`{"a":1}`))
	if err != nil {
		t.Fatalf("ScheduleJob: %v", err)
	}
	claimed1, err := ClaimNextJob(database, "queue-a", "worker-1", cfg.VisibilityTimeout)
	if err != nil {
		t.Fatalf("ClaimNextJob: %v", err)
	}
	setReservedExpiry(t, database, "queue-a", claimed1.ID, time.Now().UTC().Add(-1*time.Hour))

	// Job 2: unparseable reserved index value (should be skipped in collection).
	_, err = ScheduleJob(database, "queue-b", json.RawMessage(`{"b":2}`))
	if err != nil {
		t.Fatalf("ScheduleJob: %v", err)
	}
	claimed2, err := ClaimNextJob(database, "queue-b", "worker-2", cfg.VisibilityTimeout)
	if err != nil {
		t.Fatalf("ClaimNextJob: %v", err)
	}
	// Set an unparseable reserved index value.
	err = database.Update(func(txn *badger.Txn) error {
		return txn.Set(db.ReservedIndexKey("queue-b", claimed2.ID), []byte("not-a-number"))
	})
	if err != nil {
		t.Fatalf("set unparseable reserved value: %v", err)
	}

	// Run expireReservations.
	sweeper := NewSweeper(database, cfg)
	sweeper.expireReservations()

	// Verify job 1 was re-queued.
	var j1 Job
	err = database.View(func(txn *badger.Txn) error {
		item, err := txn.Get(db.JobKey(claimed1.ID))
		if err != nil {
			return err
		}
		data, err := item.ValueCopy(nil)
		if err != nil {
			return err
		}
		return json.Unmarshal(data, &j1)
	})
	if err != nil {
		t.Fatalf("reading job 1: %v", err)
	}
	if j1.Status != StatusPending {
		t.Errorf("job 1 status = %q, want %q", j1.Status, StatusPending)
	}

	// Verify job 2 is still reserved (unparseable index was skipped, not crashed).
	var j2 Job
	err = database.View(func(txn *badger.Txn) error {
		item, err := txn.Get(db.JobKey(claimed2.ID))
		if err != nil {
			return err
		}
		data, err := item.ValueCopy(nil)
		if err != nil {
			return err
		}
		return json.Unmarshal(data, &j2)
	})
	if err != nil {
		t.Fatalf("reading job 2: %v", err)
	}
	if j2.Status != StatusReserved {
		t.Errorf("job 2 status = %q, want %q (unparseable index should be skipped)", j2.Status, StatusReserved)
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

func TestSweeper_Start_DoesNotExpireWorkersOnStartup(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := sweepTestConfig()
	cfg.SweepInterval = 1 * time.Hour // very long ticker so only the initial pass runs

	// Register a worker.
	id, plainToken, err := RegisterWorker(database, cfg)
	if err != nil {
		t.Fatalf("RegisterWorker(): %v", err)
	}

	// Set its lastSeen to well beyond WorkerExpiry (simulating a restart
	// where the worker's durable LastSeen is stale).
	setWorkerLastSeen(t, database, id, time.Now().UTC().Add(-1*time.Hour))
	// Clear in-memory cache — the worker hasn't reconnected yet.
	workerLastSeen.Delete(id)
	workerLastSeen.Delete("flush:" + id)

	// Also set up an expired reservation to verify the startup pass
	// still handles reservation expiry.
	_, err = ScheduleJob(database, "startupqueue", json.RawMessage(`{"expired":"reservation"}`))
	if err != nil {
		t.Fatalf("ScheduleJob(): %v", err)
	}

	claimed, err := ClaimNextJob(database, "startupqueue", "some-worker", cfg.VisibilityTimeout)
	if err != nil {
		t.Fatalf("ClaimNextJob(): %v", err)
	}
	if claimed == nil {
		t.Fatal("ClaimNextJob() returned nil")
	}

	// Expire the reservation.
	setReservedExpiry(t, database, "startupqueue", claimed.ID, time.Now().UTC().Add(-1*time.Hour))

	// Start the sweeper (should run startupSweep immediately).
	sweeper := NewSweeper(database, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		sweeper.Start(ctx)
		close(done)
	}()

	// Give it time to run the initial startup pass.
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done

	// --- Worker must NOT have been expired by the startup pass ---

	// Worker record should still exist.
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.WorkerKey(id))
		return err
	})
	if err != nil {
		t.Error("worker record was deleted by startup sweep — should have been preserved")
	}

	// Token index should still exist.
	hashedToken := token.Hash(plainToken)
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.WorkerTokenKey(hashedToken))
		return err
	})
	if err != nil {
		t.Error("worker token index was deleted by startup sweep — should have been preserved")
	}

	// --- Expired reservation must still have been handled ---

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
			t.Errorf("expired reservation job status = %q, want %q (startup sweep should re-queue)", j.Status, StatusPending)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading expired reservation job: %v", err)
	}

	// Verify reserved index is gone for the expired reservation.
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.ReservedIndexKey("startupqueue", claimed.ID))
		return err
	})
	if err == nil {
		t.Error("reserved index should be gone after startup sweep expired reservation")
	}
}

// ---- Sweep + Deregister Race ----

func TestSweep_ExpiredReservation_DeregisterRace(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := sweepTestConfig()
	cfg.VisibilityTimeout = 10 * time.Second

	// Register a worker.
	workerID, _, err := RegisterWorker(database, cfg)
	if err != nil {
		t.Fatalf("RegisterWorker(): %v", err)
	}

	// Schedule and claim enough jobs to spread across multiple maintenance batches.
	const numJobs = maintenanceBatchSize + 15
	jobIDs := make([]string, 0, numJobs)

	for i := 0; i < numJobs; i++ {
		_, err := ScheduleJob(database, "racequeue", json.RawMessage(`{"n":`+fmt.Sprintf("%d", i)+`}`))
		if err != nil {
			t.Fatalf("ScheduleJob %d: %v", i, err)
		}
		claimed, err := ClaimNextJob(database, "racequeue", workerID, cfg.VisibilityTimeout)
		if err != nil {
			t.Fatalf("ClaimNextJob %d: %v", i, err)
		}
		if claimed == nil {
			t.Fatalf("ClaimNextJob %d returned nil", i)
		}
		jobIDs = append(jobIDs, claimed.ID)
	}

	// Expire all reservations so both expireReservations and deregister
	// will find work to do on the same jobs.
	for _, id := range jobIDs {
		setReservedExpiry(t, database, "racequeue", id, time.Now().UTC().Add(-1*time.Hour))
	}

	// Run deregistration and reservation expiry concurrently.
	sweeper := NewSweeper(database, cfg)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		_ = DeregisterWorker(database, workerID)
	}()

	go func() {
		defer wg.Done()
		sweeper.expireReservations()
	}()

	wg.Wait()

	// Every job must be in a consistent state: if the job record still exists
	// it must be pending with no WorkerID and valid indexes. The job may also
	// have been deleted (ack is not possible here, but we handle it gracefully).
	for _, id := range jobIDs {
		var j Job
		err := database.View(func(txn *badger.Txn) error {
			item, err := txn.Get(db.JobKey(id))
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
			// Job record deleted — valid outcome (though unlikely here).
			continue
		}
		if j.Status != StatusPending {
			t.Errorf("job %s status after race = %q, want %q", id, j.Status, StatusPending)
		}
		if j.WorkerID != "" {
			t.Errorf("job %s WorkerID after race = %q, want empty", id, j.WorkerID)
		}
		// Full invariant check.
		verifyJobInvariants(t, database, id, "racequeue")
	}

	// Verify no reserved indexes remain for any of the race jobs.
	for _, id := range jobIDs {
		if err := database.View(func(txn *badger.Txn) error {
			_, err := txn.Get(db.ReservedIndexKey("racequeue", id))
			return err
		}); err == nil {
			t.Errorf("stale reserved index for job %s after race", id)
		}
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
