//nolint:wrapcheck // test assertions pass through external errors; wrapping is noise here.
package queue

import (
	"encoding/json"
	"testing"
	"time"

	badger "github.com/dgraph-io/badger/v4"

	"github.com/mkoziy/http-queue/config"
	"github.com/mkoziy/http-queue/db"
	"github.com/mkoziy/http-queue/token"
)

// testConfig returns a minimal config.Config with test-appropriate values.
func testConfig() *config.Config {
	return &config.Config{
		AdminUser:         "testadmin",
		AdminPass:         "testpass",
		VisibilityTimeout: 30 * time.Second,
		MaxAttempts:       3,
		LastSeenDebounce:  100 * time.Millisecond,
		SweepInterval:     10 * time.Minute, // don't trigger sweeps during tests
		WorkerExpiry:      10 * time.Minute,
	}
}

// openTestDB opens a BadgerDB in a temporary directory for testing.
// Returns the cleanup function that the caller must defer.
func openTestDB(t *testing.T) (*badger.DB, func()) {
	t.Helper()

	dir := t.TempDir()
	database, err := db.Open(dir)
	if err != nil {
		t.Fatalf("db.Open(%q): %v", dir, err)
	}

	cleanup := func() {
		if err := db.Close(database); err != nil {
			t.Errorf("db.Close(): %v", err)
		}
	}

	return database, cleanup
}

// ---- Registration Tests ----

func TestRegisterWorker(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	id, plainToken, err := RegisterWorker(database, testConfig())
	if err != nil {
		t.Fatalf("RegisterWorker() error: %v", err)
	}

	if id == "" {
		t.Error("RegisterWorker() returned empty worker ID")
	}
	if plainToken == "" {
		t.Error("RegisterWorker() returned empty plain token")
	}

	// Verify the worker record exists in the database.
	err = database.View(func(txn *badger.Txn) error {
		item, err := txn.Get(db.WorkerKey(id))
		if err != nil {
			return err
		}
		data, err := item.ValueCopy(nil)
		if err != nil {
			return err
		}
		if len(data) == 0 {
			t.Error("worker record is empty")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("worker record not found: %v", err)
	}

	// Verify the token reverse index exists.
	err = database.View(func(txn *badger.Txn) error {
		hashed := token.Hash(plainToken)
		item, err := txn.Get(db.WorkerTokenKey(hashed))
		if err != nil {
			return err
		}
		val, err := item.ValueCopy(nil)
		if err != nil {
			return err
		}
		if string(val) != id {
			t.Errorf("token index points to %q, want %q", string(val), id)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("worker token index not found: %v", err)
	}
}

func TestRegisterWorker_UniqueIDs(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	ids := make(map[string]bool)
	for i := 0; i < 5; i++ {
		id, _, err := RegisterWorker(database, testConfig())
		if err != nil {
			t.Fatalf("RegisterWorker() error: %v", err)
		}
		if ids[id] {
			t.Errorf("duplicate worker ID generated: %s", id)
		}
		ids[id] = true
	}
}

func TestRegisterWorker_UniqueTokens(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	tokens := make(map[string]bool)
	for i := 0; i < 5; i++ {
		_, token, err := RegisterWorker(database, testConfig())
		if err != nil {
			t.Fatalf("RegisterWorker() error: %v", err)
		}
		if tokens[token] {
			t.Errorf("duplicate worker token generated: %s", token)
		}
		tokens[token] = true
	}
}

// ---- WorkerByToken Tests ----

func TestWorkerByToken(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	registeredID, plainToken, err := RegisterWorker(database, testConfig())
	if err != nil {
		t.Fatalf("RegisterWorker() error: %v", err)
	}

	worker, err := WorkerByToken(database, plainToken)
	if err != nil {
		t.Fatalf("WorkerByToken() error: %v", err)
	}

	if worker.ID != registeredID {
		t.Errorf("WorkerByToken returned ID %q, want %q", worker.ID, registeredID)
	}
	if worker.TokenHash == "" {
		t.Error("WorkerByToken returned worker with empty TokenHash")
	}
	if worker.RegisteredAt.IsZero() {
		t.Error("WorkerByToken returned worker with zero RegisteredAt")
	}
	if worker.LastSeen.IsZero() {
		t.Error("WorkerByToken returned worker with zero LastSeen")
	}
}

func TestWorkerByToken_InvalidToken(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	_, err := WorkerByToken(database, "this-token-does-not-exist")
	if err == nil {
		t.Fatal("WorkerByToken with invalid token: expected error, got nil")
	}
}

func TestWorkerByToken_EmptyToken(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	_, err := WorkerByToken(database, "")
	if err == nil {
		t.Fatal("WorkerByToken with empty token: expected error, got nil")
	}
}

func TestWorkerByToken_MultipleWorkers(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	// Register two workers.
	id1, token1, err := RegisterWorker(database, testConfig())
	if err != nil {
		t.Fatalf("RegisterWorker 1: %v", err)
	}

	id2, token2, err := RegisterWorker(database, testConfig())
	if err != nil {
		t.Fatalf("RegisterWorker 2: %v", err)
	}

	// Lookup worker 1 by its token.
	w1, err := WorkerByToken(database, token1)
	if err != nil {
		t.Fatalf("WorkerByToken(token1): %v", err)
	}
	if w1.ID != id1 {
		t.Errorf("WorkerByToken(token1) returned ID %q, want %q", w1.ID, id1)
	}

	// Lookup worker 2 by its token.
	w2, err := WorkerByToken(database, token2)
	if err != nil {
		t.Fatalf("WorkerByToken(token2): %v", err)
	}
	if w2.ID != id2 {
		t.Errorf("WorkerByToken(token2) returned ID %q, want %q", w2.ID, id2)
	}

	// Verify they are different.
	if w1.ID == w2.ID {
		t.Error("two registered workers should have different IDs")
	}
}

// ---- DeregisterWorker Tests ----

func TestDeregisterWorker(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	id, _, err := RegisterWorker(database, testConfig())
	if err != nil {
		t.Fatalf("RegisterWorker() error: %v", err)
	}

	err = DeregisterWorker(database, id)
	if err != nil {
		t.Fatalf("DeregisterWorker() error: %v", err)
	}

	// Verify worker record is gone.
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.WorkerKey(id))
		return err
	})
	if err == nil {
		t.Error("worker record still exists after deregistration")
	}
}

func TestDeregisterWorker_TokenIndexCleaned(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	id, plainToken, err := RegisterWorker(database, testConfig())
	if err != nil {
		t.Fatalf("RegisterWorker() error: %v", err)
	}

	hashed := token.Hash(plainToken)

	err = DeregisterWorker(database, id)
	if err != nil {
		t.Fatalf("DeregisterWorker() error: %v", err)
	}

	// Verify token index is gone.
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.WorkerTokenKey(hashed))
		return err
	})
	if err == nil {
		t.Error("worker token index still exists after deregistration")
	}
}

func TestDeregisterWorker_InvalidID(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	err := DeregisterWorker(database, "nonexistent-worker-id")
	if err == nil {
		t.Fatal("DeregisterWorker with invalid ID: expected error, got nil")
	}
}

func TestDeregisterWorker_Twice(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	id, _, err := RegisterWorker(database, testConfig())
	if err != nil {
		t.Fatalf("RegisterWorker() error: %v", err)
	}

	if err := DeregisterWorker(database, id); err != nil {
		t.Fatalf("First DeregisterWorker: %v", err)
	}

	err = DeregisterWorker(database, id)
	if err == nil {
		t.Fatal("Second DeregisterWorker: expected error, got nil")
	}
}

func TestDeregisterWorker_RequeuesReservedJobs(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()

	// Register a worker.
	id, _, err := RegisterWorker(database, cfg)
	if err != nil {
		t.Fatalf("RegisterWorker(): %v", err)
	}

	// Schedule a job.
	_, err = ScheduleJob(database, "testqueue", []byte(`{"hello":"world"}`))
	if err != nil {
		t.Fatalf("ScheduleJob(): %v", err)
	}

	// Claim the job (transition to reserved, owned by the worker).
	claimed, err := ClaimNextJob(database, "testqueue", id, cfg.VisibilityTimeout)
	if err != nil {
		t.Fatalf("ClaimNextJob(): %v", err)
	}
	if claimed == nil {
		t.Fatal("ClaimNextJob returned nil, expected a job")
	}

	// Deregister the worker — this should re-queue the reserved job.
	if err := DeregisterWorker(database, id); err != nil {
		t.Fatalf("DeregisterWorker(): %v", err)
	}

	// Verify the job is back in pending state.
	var status JobStatus
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
		status = j.Status
		if j.WorkerID != "" {
			t.Errorf("job WorkerID = %q, want empty after deregistration", j.WorkerID)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading job: %v", err)
	}
	if status != StatusPending {
		t.Errorf("job status = %q, want %q after deregistration", status, StatusPending)
	}

	// Verify the pending index exists.
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.PendingIndexKey("testqueue", claimed.ID))
		return err
	})
	if err != nil {
		t.Error("pending index missing after worker deregistration (job should be re-queued)")
	}

	// Verify reserved index is gone.
	err = database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(db.ReservedIndexKey("testqueue", claimed.ID))
		return err
	})
	if err == nil {
		t.Error("reserved index still exists after worker deregistration")
	}
}

// ---- TouchWorker Tests ----

func TestTouchWorker(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	id, plainToken, err := RegisterWorker(database, testConfig())
	if err != nil {
		t.Fatalf("RegisterWorker() error: %v", err)
	}

	// Record initial last-seen.
	worker, err := WorkerByToken(database, plainToken)
	if err != nil {
		t.Fatalf("WorkerByToken() error: %v", err)
	}
	initialLastSeen := worker.LastSeen

	// Wait a tiny bit so the timestamp differs.
	time.Sleep(5 * time.Millisecond)

	// Touch the worker.
	TouchWorker(database, id, testConfig().LastSeenDebounce)

	// Read worker and verify last-seen was updated.
	updated, err := WorkerByToken(database, plainToken)
	if err != nil {
		t.Fatalf("WorkerByToken() error after TouchWorker: %v", err)
	}

	if !updated.LastSeen.After(initialLastSeen) {
		t.Errorf("TouchWorker did not update LastSeen: was %v, now %v", initialLastSeen, updated.LastSeen)
	}
}

func TestTouchWorker_Debounce(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	id, plainToken, err := RegisterWorker(database, cfg)
	if err != nil {
		t.Fatalf("RegisterWorker() error: %v", err)
	}

	// Read initial last-seen.
	worker, err := WorkerByToken(database, plainToken)
	if err != nil {
		t.Fatalf("WorkerByToken() error: %v", err)
	}
	initialLastSeen := worker.LastSeen

	// Small delay so initial timestamp is behind.
	time.Sleep(2 * time.Millisecond)

	// Touch worker multiple times rapidly (within debounce window).
	for i := 0; i < 10; i++ {
		TouchWorker(database, id, cfg.LastSeenDebounce) // debounce is 100ms
	}

	// Verify last-seen was updated at least once (in-memory cache always updates).
	// But the BadgerDB value may not be flushed until debounce expires.
	// Read the in-memory value to verify it's been touched.
	val, ok := workerLastSeen.Load(id)
	if !ok {
		t.Fatal("worker not in in-memory last-seen cache")
	}
	cachedTime, ok := val.(time.Time)
	if !ok {
		t.Fatal("in-memory last-seen is not a time.Time")
	}
	if !cachedTime.After(initialLastSeen) {
		t.Errorf("in-memory last-seen not updated after TouchWorker: was %v, now %v", initialLastSeen, cachedTime)
	}

	// Wait for debounce to expire.
	time.Sleep(cfg.LastSeenDebounce + 50*time.Millisecond)

	// Touch again — this should flush to BadgerDB.
	TouchWorker(database, id, cfg.LastSeenDebounce)

	// Verify the DB value is now updated.
	updated, err := WorkerByToken(database, plainToken)
	if err != nil {
		t.Fatalf("WorkerByToken() error: %v", err)
	}
	if !updated.LastSeen.After(initialLastSeen) {
		t.Errorf("BadgerDB last-seen not updated after debounce expired: was %v, now %v", initialLastSeen, updated.LastSeen)
	}
}

func TestTouchWorker_Nonexistent(t *testing.T) {
	// TouchWorker should not panic if the worker doesn't exist.
	// It will log an error but not crash.
	database, cleanup := openTestDB(t)
	defer cleanup()

	// This should not panic.
	TouchWorker(database, "nonexistent-worker", 100*time.Millisecond)
}

// ---- Edge Cases ----

func TestWorkerByToken_AfterDeregister(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	id, plainToken, err := RegisterWorker(database, testConfig())
	if err != nil {
		t.Fatalf("RegisterWorker() error: %v", err)
	}

	if err := DeregisterWorker(database, id); err != nil {
		t.Fatalf("DeregisterWorker() error: %v", err)
	}

	// Token lookup should now fail.
	_, err = WorkerByToken(database, plainToken)
	if err == nil {
		t.Fatal("WorkerByToken after deregister: expected error, got nil")
	}
}

func TestDeregisterWorker_OnlyRequeuesOwnedJobs(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()

	// Register two workers.
	id1, _, err := RegisterWorker(database, cfg)
	if err != nil {
		t.Fatalf("RegisterWorker 1: %v", err)
	}

	id2, _, err := RegisterWorker(database, cfg)
	if err != nil {
		t.Fatalf("RegisterWorker 2: %v", err)
	}

	// Schedule two jobs.
	_, err = ScheduleJob(database, "testqueue", []byte(`{"job":1}`))
	if err != nil {
		t.Fatalf("ScheduleJob 1: %v", err)
	}

	_, err = ScheduleJob(database, "testqueue", []byte(`{"job":2}`))
	if err != nil {
		t.Fatalf("ScheduleJob 2: %v", err)
	}

	// Worker 1 claims job 1.
	claimed1, err := ClaimNextJob(database, "testqueue", id1, cfg.VisibilityTimeout)
	if err != nil {
		t.Fatalf("ClaimNextJob worker1: %v", err)
	}
	if claimed1 == nil {
		t.Fatal("ClaimNextJob worker1 returned nil")
	}

	// Worker 2 claims job 2.
	claimed2, err := ClaimNextJob(database, "testqueue", id2, cfg.VisibilityTimeout)
	if err != nil {
		t.Fatalf("ClaimNextJob worker2: %v", err)
	}
	if claimed2 == nil {
		t.Fatal("ClaimNextJob worker2 returned nil")
	}

	// Deregister worker 1 only.
	if err := DeregisterWorker(database, id1); err != nil {
		t.Fatalf("DeregisterWorker worker1: %v", err)
	}

	// Job 1 should be re-queued (pending, no owner).
	err = database.View(func(txn *badger.Txn) error {
		item, err := txn.Get(db.JobKey(claimed1.ID))
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
			t.Errorf("job1 status = %q, want %q", j.Status, StatusPending)
		}
		if j.WorkerID != "" {
			t.Errorf("job1 WorkerID = %q, want empty", j.WorkerID)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading job1: %v", err)
	}

	// Job 2 should still be reserved by worker 2.
	err = database.View(func(txn *badger.Txn) error {
		item, err := txn.Get(db.JobKey(claimed2.ID))
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
			t.Errorf("job2 status = %q, want %q", j.Status, StatusReserved)
		}
		if j.WorkerID != id2 {
			t.Errorf("job2 WorkerID = %q, want %q", j.WorkerID, id2)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading job2: %v", err)
	}
}

func TestDeregisterWorker_RequeuesMultipleJobs(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()

	// Register worker.
	id, _, err := RegisterWorker(database, cfg)
	if err != nil {
		t.Fatalf("RegisterWorker(): %v", err)
	}

	// Schedule and claim multiple jobs.
	const numJobs = 5
	claimedIDs := make(map[string]bool)
	for i := 0; i < numJobs; i++ {
		job, err := ScheduleJob(database, "testqueue", []byte(`{"n":`+string(rune('0'+i))+`}`))
		if err != nil {
			t.Fatalf("ScheduleJob %d: %v", i, err)
		}
		claimed, err := ClaimNextJob(database, "testqueue", id, cfg.VisibilityTimeout)
		if err != nil {
			t.Fatalf("ClaimNextJob %d: %v", i, err)
		}
		if claimed == nil {
			t.Fatalf("ClaimNextJob %d returned nil", i)
		}
		// Skip duplicate ULIDs caused by the test iteration (just in case).
		_ = job
		claimedIDs[claimed.ID] = true
	}

	// Deregister.
	if err := DeregisterWorker(database, id); err != nil {
		t.Fatalf("DeregisterWorker(): %v", err)
	}

	// All claimed jobs should be back to pending.
	for jobID := range claimedIDs {
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
		if j.Status != StatusPending {
			t.Errorf("job %s status = %q, want %q", jobID, j.Status, StatusPending)
		}
		if j.WorkerID != "" {
			t.Errorf("job %s WorkerID = %q, want empty", jobID, j.WorkerID)
		}
	}
}

// ---- In-Memory LastSeen Cache Tests ----

func TestWorkerLastSeen_Initialized(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	id, _, err := RegisterWorker(database, testConfig())
	if err != nil {
		t.Fatalf("RegisterWorker() error: %v", err)
	}

	// Verify the in-memory cache was initialized.
	val, ok := workerLastSeen.Load(id)
	if !ok {
		t.Fatal("worker not in in-memory last-seen cache after registration")
	}
	if _, ok := val.(time.Time); !ok {
		t.Fatal("in-memory last-seen value is not a time.Time")
	}
}

func TestWorkerLastSeen_CleanedOnDeregister(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	id, _, err := RegisterWorker(database, testConfig())
	if err != nil {
		t.Fatalf("RegisterWorker() error: %v", err)
	}

	// Verify the cache has the worker.
	_, ok := workerLastSeen.Load(id)
	if !ok {
		t.Fatal("worker should be in in-memory cache after registration")
	}

	if err := DeregisterWorker(database, id); err != nil {
		t.Fatalf("DeregisterWorker(): %v", err)
	}

	// Verify the cache no longer has the worker.
	_, ok = workerLastSeen.Load(id)
	if ok {
		t.Error("worker still in in-memory cache after deregistration")
	}
}
