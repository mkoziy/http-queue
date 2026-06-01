//nolint:wrapcheck // test assertions pass through external errors; wrapping is noise here.
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---- HandleClaimNextJob Tests ----

func TestHandleClaimNextJob_Success(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	// Register a worker.
	regReq := httptest.NewRequest(http.MethodPost, "/workers", nil)
	regReq.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	regRec := httptest.NewRecorder()
	router.ServeHTTP(regRec, regReq)
	if regRec.Code != http.StatusCreated {
		t.Fatalf("register worker: status = %d, want %d", regRec.Code, http.StatusCreated)
	}
	var regResp map[string]string
	if err := json.NewDecoder(regRec.Body).Decode(&regResp); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	workerToken := regResp["token"]

	// Schedule a job.
	jobBody := `{"payload":{"hello":"world"}}`
	jobReq := httptest.NewRequest(http.MethodPost, "/queues/testqueue/jobs", strings.NewReader(jobBody))
	jobReq.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	jobRec := httptest.NewRecorder()
	router.ServeHTTP(jobRec, jobReq)
	if jobRec.Code != http.StatusCreated {
		t.Fatalf("schedule job: status = %d, want %d", jobRec.Code, http.StatusCreated)
	}
	var jobResp map[string]interface{}
	if err := json.NewDecoder(jobRec.Body).Decode(&jobResp); err != nil {
		t.Fatalf("decode job response: %v", err)
	}
	jobID := jobResp["id"].(string)

	// Claim the job as the worker.
	claimReq := httptest.NewRequest(http.MethodGet, "/queues/testqueue/next", nil)
	claimReq.Header.Set("Authorization", "Bearer "+workerToken)
	claimRec := httptest.NewRecorder()
	router.ServeHTTP(claimRec, claimReq)

	if claimRec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body = %q", claimRec.Code, http.StatusOK, claimRec.Body.String())
	}

	var claimResp map[string]interface{}
	if err := json.NewDecoder(claimRec.Body).Decode(&claimResp); err != nil {
		t.Fatalf("decode claim response: %v", err)
	}

	if claimResp["id"] != jobID {
		t.Errorf("claimed job ID = %v, want %q", claimResp["id"], jobID)
	}
	if claimResp["queue"] != "testqueue" {
		t.Errorf("queue = %v, want %q", claimResp["queue"], "testqueue")
	}
	if claimResp["attempts"] != nil {
		attempts := int(claimResp["attempts"].(float64))
		if attempts != 1 {
			t.Errorf("attempts = %d, want 1", attempts)
		}
	}
}

func TestHandleClaimNextJob_EmptyQueue(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	// Register a worker.
	regReq := httptest.NewRequest(http.MethodPost, "/workers", nil)
	regReq.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	regRec := httptest.NewRecorder()
	router.ServeHTTP(regRec, regReq)
	if regRec.Code != http.StatusCreated {
		t.Fatalf("register worker: status = %d, want %d", regRec.Code, http.StatusCreated)
	}
	var regResp map[string]string
	if err := json.NewDecoder(regRec.Body).Decode(&regResp); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	workerToken := regResp["token"]

	// Try to claim from an empty queue.
	claimReq := httptest.NewRequest(http.MethodGet, "/queues/emptyqueue/next", nil)
	claimReq.Header.Set("Authorization", "Bearer "+workerToken)
	claimRec := httptest.NewRecorder()
	router.ServeHTTP(claimRec, claimReq)

	if claimRec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d; body = %q", claimRec.Code, http.StatusNoContent, claimRec.Body.String())
	}
}

func TestHandleClaimNextJob_MissingQueueName(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	// Register a worker.
	regReq := httptest.NewRequest(http.MethodPost, "/workers", nil)
	regReq.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	regRec := httptest.NewRecorder()
	router.ServeHTTP(regRec, regReq)
	if regRec.Code != http.StatusCreated {
		t.Fatalf("register worker: status = %d, want %d", regRec.Code, http.StatusCreated)
	}
	var regResp map[string]string
	if err := json.NewDecoder(regRec.Body).Decode(&regResp); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	workerToken := regResp["token"]

	// An empty queue name in the path causes Go's ServeMux to clean the URL
	// and issue a 307 redirect. The handler is not reached.
	claimReq := httptest.NewRequest(http.MethodGet, "/queues//next", nil)
	claimReq.Header.Set("Authorization", "Bearer "+workerToken)
	claimRec := httptest.NewRecorder()
	router.ServeHTTP(claimRec, claimReq)

	if claimRec.Code != http.StatusTemporaryRedirect {
		t.Errorf("status = %d, want %d; body = %q", claimRec.Code, http.StatusTemporaryRedirect, claimRec.Body.String())
	}
}

func TestHandleClaimNextJob_NoAuth(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	// No auth header.
	req := httptest.NewRequest(http.MethodGet, "/queues/testqueue/next", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestHandleClaimNextJob_WrongAuth(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	// Using Basic auth instead of Bearer.
	req := httptest.NewRequest(http.MethodGet, "/queues/testqueue/next", nil)
	req.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestHandleClaimNextJob_InvalidToken(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	// Invalid bearer token.
	req := httptest.NewRequest(http.MethodGet, "/queues/testqueue/next", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestHandleClaimNextJob_WrongMethod(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	req := httptest.NewRequest(http.MethodPost, "/queues/testqueue/next", nil)
	req.Header.Set("Authorization", "Bearer some-token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleClaimNextJob_ContentType(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	// Register a worker.
	regReq := httptest.NewRequest(http.MethodPost, "/workers", nil)
	regReq.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	regRec := httptest.NewRecorder()
	router.ServeHTTP(regRec, regReq)
	if regRec.Code != http.StatusCreated {
		t.Fatalf("register worker: status = %d, want %d", regRec.Code, http.StatusCreated)
	}
	var regResp map[string]string
	if err := json.NewDecoder(regRec.Body).Decode(&regResp); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	workerToken := regResp["token"]

	// Schedule a job.
	jobBody := `{"payload":{"hello":"world"}}`
	jobReq := httptest.NewRequest(http.MethodPost, "/queues/testqueue/jobs", strings.NewReader(jobBody))
	jobReq.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	jobRec := httptest.NewRecorder()
	router.ServeHTTP(jobRec, jobReq)

	// Claim and check Content-Type.
	claimReq := httptest.NewRequest(http.MethodGet, "/queues/testqueue/next", nil)
	claimReq.Header.Set("Authorization", "Bearer "+workerToken)
	claimRec := httptest.NewRecorder()
	router.ServeHTTP(claimRec, claimReq)

	if ct := claimRec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

// ---- HandleAckJob Tests ----

func TestHandleAckJob_Success(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	// Register a worker.
	_, workerToken := registerTestWorker(t, database)

	// Schedule a job.
	jobBody := `{"payload":{"hello":"world"}}`
	jobReq := httptest.NewRequest(http.MethodPost, "/queues/testqueue/jobs", strings.NewReader(jobBody))
	jobReq.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	jobRec := httptest.NewRecorder()
	router.ServeHTTP(jobRec, jobReq)
	if jobRec.Code != http.StatusCreated {
		t.Fatalf("schedule job: status = %d, want %d", jobRec.Code, http.StatusCreated)
	}
	var jobResp map[string]interface{}
	if err := json.NewDecoder(jobRec.Body).Decode(&jobResp); err != nil {
		t.Fatalf("decode job response: %v", err)
	}
	jobID := jobResp["id"].(string)

	// Claim the job.
	claimReq := httptest.NewRequest(http.MethodGet, "/queues/testqueue/next", nil)
	claimReq.Header.Set("Authorization", "Bearer "+workerToken)
	claimRec := httptest.NewRecorder()
	router.ServeHTTP(claimRec, claimReq)
	if claimRec.Code != http.StatusOK {
		t.Fatalf("claim job: status = %d, want %d", claimRec.Code, http.StatusOK)
	}

	// Ack the job.
	ackReq := httptest.NewRequest(http.MethodPost, "/jobs/"+jobID+"/ack", nil)
	ackReq.Header.Set("Authorization", "Bearer "+workerToken)
	ackRec := httptest.NewRecorder()
	router.ServeHTTP(ackRec, ackReq)

	if ackRec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d; body = %q", ackRec.Code, http.StatusNoContent, ackRec.Body.String())
	}
}

func TestHandleAckJob_AfterTTLExpiryStillSucceeds(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	_, workerToken := registerTestWorker(t, database)

	jobBody := `{"payload":{"hello":"world"},"ttl":1}`
	jobReq := httptest.NewRequest(http.MethodPost, "/queues/testqueue/jobs", strings.NewReader(jobBody))
	jobReq.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	jobRec := httptest.NewRecorder()
	router.ServeHTTP(jobRec, jobReq)
	if jobRec.Code != http.StatusCreated {
		t.Fatalf("schedule job: status = %d, want %d", jobRec.Code, http.StatusCreated)
	}
	var jobResp map[string]interface{}
	if err := json.NewDecoder(jobRec.Body).Decode(&jobResp); err != nil {
		t.Fatalf("decode job response: %v", err)
	}
	jobID := jobResp["id"].(string)

	claimReq := httptest.NewRequest(http.MethodGet, "/queues/testqueue/next", nil)
	claimReq.Header.Set("Authorization", "Bearer "+workerToken)
	claimRec := httptest.NewRecorder()
	router.ServeHTTP(claimRec, claimReq)
	if claimRec.Code != http.StatusOK {
		t.Fatalf("claim job: status = %d, want %d", claimRec.Code, http.StatusOK)
	}

	time.Sleep(1100 * time.Millisecond)

	ackReq := httptest.NewRequest(http.MethodPost, "/jobs/"+jobID+"/ack", nil)
	ackReq.Header.Set("Authorization", "Bearer "+workerToken)
	ackRec := httptest.NewRecorder()
	router.ServeHTTP(ackRec, ackReq)

	if ackRec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d; body = %q", ackRec.Code, http.StatusNoContent, ackRec.Body.String())
	}
}

func TestHandleAckJob_DoubleAck(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	// Register a worker.
	_, workerToken := registerTestWorker(t, database)

	// Schedule and claim a job.
	jobBody := `{"payload":{}}`
	jobReq := httptest.NewRequest(http.MethodPost, "/queues/testqueue/jobs", strings.NewReader(jobBody))
	jobReq.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	jobRec := httptest.NewRecorder()
	router.ServeHTTP(jobRec, jobReq)
	var jobResp map[string]interface{}
	if err := json.NewDecoder(jobRec.Body).Decode(&jobResp); err != nil {
		t.Fatalf("decode job response: %v", err)
	}
	jobID := jobResp["id"].(string)

	claimReq := httptest.NewRequest(http.MethodGet, "/queues/testqueue/next", nil)
	claimReq.Header.Set("Authorization", "Bearer "+workerToken)
	claimRec := httptest.NewRecorder()
	router.ServeHTTP(claimRec, claimReq)
	if claimRec.Code != http.StatusOK {
		t.Fatalf("claim job: status = %d, want %d", claimRec.Code, http.StatusOK)
	}

	// First ack should succeed.
	ackReq1 := httptest.NewRequest(http.MethodPost, "/jobs/"+jobID+"/ack", nil)
	ackReq1.Header.Set("Authorization", "Bearer "+workerToken)
	ackRec1 := httptest.NewRecorder()
	router.ServeHTTP(ackRec1, ackReq1)
	if ackRec1.Code != http.StatusNoContent {
		t.Fatalf("first ack: status = %d, want %d", ackRec1.Code, http.StatusNoContent)
	}

	// Second ack should fail (job no longer exists).
	ackReq2 := httptest.NewRequest(http.MethodPost, "/jobs/"+jobID+"/ack", nil)
	ackReq2.Header.Set("Authorization", "Bearer "+workerToken)
	ackRec2 := httptest.NewRecorder()
	router.ServeHTTP(ackRec2, ackReq2)
	if ackRec2.Code != http.StatusBadRequest {
		t.Errorf("second ack: status = %d, want %d; body = %q", ackRec2.Code, http.StatusBadRequest, ackRec2.Body.String())
	}

	var errResp map[string]string
	if err := json.NewDecoder(ackRec2.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if errResp["error"] == "" {
		t.Error("expected error message in response")
	}
}

func TestHandleAckJob_NotOwner(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	// Register two workers.
	_, token1 := registerTestWorker(t, database)
	_, token2 := registerTestWorker(t, database)

	// Schedule a job.
	jobBody := `{"payload":{}}`
	jobReq := httptest.NewRequest(http.MethodPost, "/queues/testqueue/jobs", strings.NewReader(jobBody))
	jobReq.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	jobRec := httptest.NewRecorder()
	router.ServeHTTP(jobRec, jobReq)
	var jobResp map[string]interface{}
	if err := json.NewDecoder(jobRec.Body).Decode(&jobResp); err != nil {
		t.Fatalf("decode job response: %v", err)
	}
	jobID := jobResp["id"].(string)

	// Worker 1 claims the job.
	claimReq := httptest.NewRequest(http.MethodGet, "/queues/testqueue/next", nil)
	claimReq.Header.Set("Authorization", "Bearer "+token1)
	claimRec := httptest.NewRecorder()
	router.ServeHTTP(claimRec, claimReq)
	if claimRec.Code != http.StatusOK {
		t.Fatalf("claim job: status = %d, want %d", claimRec.Code, http.StatusOK)
	}

	// Worker 2 tries to ack the job (should fail).
	ackReq := httptest.NewRequest(http.MethodPost, "/jobs/"+jobID+"/ack", nil)
	ackReq.Header.Set("Authorization", "Bearer "+token2)
	ackRec := httptest.NewRecorder()
	router.ServeHTTP(ackRec, ackReq)
	if ackRec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d; body = %q", ackRec.Code, http.StatusBadRequest, ackRec.Body.String())
	}

	var errResp map[string]string
	if err := json.NewDecoder(ackRec.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if errResp["error"] == "" {
		t.Error("expected error message in response")
	}
}

func TestHandleAckJob_Nonexistent(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	_, workerToken := registerTestWorker(t, database)

	// Ack a non-existent job.
	ackReq := httptest.NewRequest(http.MethodPost, "/jobs/nonexistent-job-id/ack", nil)
	ackReq.Header.Set("Authorization", "Bearer "+workerToken)
	ackRec := httptest.NewRecorder()
	router.ServeHTTP(ackRec, ackReq)

	if ackRec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d; body = %q", ackRec.Code, http.StatusBadRequest, ackRec.Body.String())
	}

	var errResp map[string]string
	if err := json.NewDecoder(ackRec.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if errResp["error"] == "" {
		t.Error("expected error message in response")
	}
}

func TestHandleAckJob_MissingJobID(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	_, workerToken := registerTestWorker(t, database)

	// An empty job ID in the path causes Go's ServeMux to clean the URL
	// and issue a 307 redirect. The handler is not reached.
	req := httptest.NewRequest(http.MethodPost, "/jobs//ack", nil)
	req.Header.Set("Authorization", "Bearer "+workerToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusTemporaryRedirect {
		t.Errorf("status = %d, want %d; body = %q", rec.Code, http.StatusTemporaryRedirect, rec.Body.String())
	}
}

func TestHandleAckJob_NoAuth(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	req := httptest.NewRequest(http.MethodPost, "/jobs/some-id/ack", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestHandleAckJob_WrongMethod(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	// GET instead of POST.
	req := httptest.NewRequest(http.MethodGet, "/jobs/some-id/ack", nil)
	req.Header.Set("Authorization", "Bearer some-token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

// ---- HandleNackJob Tests ----

func TestHandleNackJob_RequeuesJob(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	// Register a worker.
	_, workerToken := registerTestWorker(t, database)

	// Schedule a job.
	jobBody := `{"payload":{"hello":"world"}}`
	jobReq := httptest.NewRequest(http.MethodPost, "/queues/testqueue/jobs", strings.NewReader(jobBody))
	jobReq.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	jobRec := httptest.NewRecorder()
	router.ServeHTTP(jobRec, jobReq)
	if jobRec.Code != http.StatusCreated {
		t.Fatalf("schedule job: status = %d, want %d", jobRec.Code, http.StatusCreated)
	}
	var jobResp map[string]interface{}
	if err := json.NewDecoder(jobRec.Body).Decode(&jobResp); err != nil {
		t.Fatalf("decode job response: %v", err)
	}
	jobID := jobResp["id"].(string)

	// Claim the job.
	claimReq := httptest.NewRequest(http.MethodGet, "/queues/testqueue/next", nil)
	claimReq.Header.Set("Authorization", "Bearer "+workerToken)
	claimRec := httptest.NewRecorder()
	router.ServeHTTP(claimRec, claimReq)
	if claimRec.Code != http.StatusOK {
		t.Fatalf("claim job: status = %d, want %d", claimRec.Code, http.StatusOK)
	}

	// Nack the job.
	nackReq := httptest.NewRequest(http.MethodPost, "/jobs/"+jobID+"/nack", nil)
	nackReq.Header.Set("Authorization", "Bearer "+workerToken)
	nackRec := httptest.NewRecorder()
	router.ServeHTTP(nackRec, nackReq)

	if nackRec.Code != http.StatusNoContent {
		t.Errorf("nack: status = %d, want %d; body = %q", nackRec.Code, http.StatusNoContent, nackRec.Body.String())
	}

	// The job should be re-queued; claim it again.
	claimReq2 := httptest.NewRequest(http.MethodGet, "/queues/testqueue/next", nil)
	claimReq2.Header.Set("Authorization", "Bearer "+workerToken)
	claimRec2 := httptest.NewRecorder()
	router.ServeHTTP(claimRec2, claimReq2)

	if claimRec2.Code != http.StatusOK {
		t.Errorf("re-claim after nack: status = %d, want %d; body = %q", claimRec2.Code, http.StatusOK, claimRec2.Body.String())
	}

	var reClaimed map[string]interface{}
	if err := json.NewDecoder(claimRec2.Body).Decode(&reClaimed); err != nil {
		t.Fatalf("decode re-claimed response: %v", err)
	}
	if reClaimed["id"] != jobID {
		t.Errorf("re-claimed job ID = %v, want %q", reClaimed["id"], jobID)
	}
	// Attempts should be 2 now.
	if reClaimed["attempts"] != nil {
		attempts := int(reClaimed["attempts"].(float64))
		if attempts != 2 {
			t.Errorf("attempts = %d, want 2", attempts)
		}
	}
}

func TestHandleNackJob_AfterTTLExpiryDeletesJob(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	_, workerToken := registerTestWorker(t, database)

	jobBody := `{"payload":{"hello":"world"},"ttl":1}`
	jobReq := httptest.NewRequest(http.MethodPost, "/queues/testqueue/jobs", strings.NewReader(jobBody))
	jobReq.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	jobRec := httptest.NewRecorder()
	router.ServeHTTP(jobRec, jobReq)
	if jobRec.Code != http.StatusCreated {
		t.Fatalf("schedule job: status = %d, want %d", jobRec.Code, http.StatusCreated)
	}
	var jobResp map[string]interface{}
	if err := json.NewDecoder(jobRec.Body).Decode(&jobResp); err != nil {
		t.Fatalf("decode job response: %v", err)
	}
	jobID := jobResp["id"].(string)

	claimReq := httptest.NewRequest(http.MethodGet, "/queues/testqueue/next", nil)
	claimReq.Header.Set("Authorization", "Bearer "+workerToken)
	claimRec := httptest.NewRecorder()
	router.ServeHTTP(claimRec, claimReq)
	if claimRec.Code != http.StatusOK {
		t.Fatalf("claim job: status = %d, want %d", claimRec.Code, http.StatusOK)
	}

	time.Sleep(1100 * time.Millisecond)

	nackReq := httptest.NewRequest(http.MethodPost, "/jobs/"+jobID+"/nack", nil)
	nackReq.Header.Set("Authorization", "Bearer "+workerToken)
	nackRec := httptest.NewRecorder()
	router.ServeHTTP(nackRec, nackReq)

	if nackRec.Code != http.StatusNoContent {
		t.Fatalf("nack: status = %d, want %d; body = %q", nackRec.Code, http.StatusNoContent, nackRec.Body.String())
	}

	claimReq2 := httptest.NewRequest(http.MethodGet, "/queues/testqueue/next", nil)
	claimReq2.Header.Set("Authorization", "Bearer "+workerToken)
	claimRec2 := httptest.NewRecorder()
	router.ServeHTTP(claimRec2, claimReq2)

	if claimRec2.Code != http.StatusNoContent {
		t.Errorf("re-claim after expired nack: status = %d, want %d; body = %q", claimRec2.Code, http.StatusNoContent, claimRec2.Body.String())
	}
}

func TestHandleNackJob_MaxAttemptsMovesToDeadLetter(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	// Set max attempts to 1 so the first nack moves to dead-letter.
	cfg.MaxAttempts = 1

	router := New(database, cfg)

	// Register a worker.
	_, workerToken := registerTestWorker(t, database)

	// Schedule a job.
	jobBody := `{"payload":{"test":"dead-letter"}}`
	jobReq := httptest.NewRequest(http.MethodPost, "/queues/testqueue/jobs", strings.NewReader(jobBody))
	jobReq.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	jobRec := httptest.NewRecorder()
	router.ServeHTTP(jobRec, jobReq)
	if jobRec.Code != http.StatusCreated {
		t.Fatalf("schedule job: status = %d, want %d", jobRec.Code, http.StatusCreated)
	}
	var jobResp map[string]interface{}
	if err := json.NewDecoder(jobRec.Body).Decode(&jobResp); err != nil {
		t.Fatalf("decode job response: %v", err)
	}
	jobID := jobResp["id"].(string)

	// Claim the job (attempt becomes 1).
	claimReq := httptest.NewRequest(http.MethodGet, "/queues/testqueue/next", nil)
	claimReq.Header.Set("Authorization", "Bearer "+workerToken)
	claimRec := httptest.NewRecorder()
	router.ServeHTTP(claimRec, claimReq)
	if claimRec.Code != http.StatusOK {
		t.Fatalf("claim job: status = %d, want %d", claimRec.Code, http.StatusOK)
	}

	// Nack the job (MaxAttempts=1, so it moves to dead-letter).
	nackReq := httptest.NewRequest(http.MethodPost, "/jobs/"+jobID+"/nack", nil)
	nackReq.Header.Set("Authorization", "Bearer "+workerToken)
	nackRec := httptest.NewRecorder()
	router.ServeHTTP(nackRec, nackReq)

	if nackRec.Code != http.StatusNoContent {
		t.Errorf("nack: status = %d, want %d; body = %q", nackRec.Code, http.StatusNoContent, nackRec.Body.String())
	}

	// The job should NOT be claimable again (it's in dead-letter).
	claimReq2 := httptest.NewRequest(http.MethodGet, "/queues/testqueue/next", nil)
	claimReq2.Header.Set("Authorization", "Bearer "+workerToken)
	claimRec2 := httptest.NewRecorder()
	router.ServeHTTP(claimRec2, claimReq2)

	if claimRec2.Code != http.StatusNoContent {
		t.Errorf("expected no content for dead-letter queue, got status = %d; body = %q", claimRec2.Code, claimRec2.Body.String())
	}
}

func TestHandleNackJob_NotOwner(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	// Register two workers.
	_, token1 := registerTestWorker(t, database)
	_, token2 := registerTestWorker(t, database)

	// Schedule a job.
	jobBody := `{"payload":{}}`
	jobReq := httptest.NewRequest(http.MethodPost, "/queues/testqueue/jobs", strings.NewReader(jobBody))
	jobReq.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	jobRec := httptest.NewRecorder()
	router.ServeHTTP(jobRec, jobReq)
	var jobResp map[string]interface{}
	if err := json.NewDecoder(jobRec.Body).Decode(&jobResp); err != nil {
		t.Fatalf("decode job response: %v", err)
	}
	jobID := jobResp["id"].(string)

	// Worker 1 claims the job.
	claimReq := httptest.NewRequest(http.MethodGet, "/queues/testqueue/next", nil)
	claimReq.Header.Set("Authorization", "Bearer "+token1)
	claimRec := httptest.NewRecorder()
	router.ServeHTTP(claimRec, claimReq)
	if claimRec.Code != http.StatusOK {
		t.Fatalf("claim job: status = %d, want %d", claimRec.Code, http.StatusOK)
	}

	// Worker 2 tries to nack (should fail).
	nackReq := httptest.NewRequest(http.MethodPost, "/jobs/"+jobID+"/nack", nil)
	nackReq.Header.Set("Authorization", "Bearer "+token2)
	nackRec := httptest.NewRecorder()
	router.ServeHTTP(nackRec, nackReq)

	if nackRec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d; body = %q", nackRec.Code, http.StatusBadRequest, nackRec.Body.String())
	}

	var errResp map[string]string
	if err := json.NewDecoder(nackRec.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if errResp["error"] == "" {
		t.Error("expected error message in response")
	}
}

func TestHandleNackJob_Nonexistent(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	_, workerToken := registerTestWorker(t, database)

	nackReq := httptest.NewRequest(http.MethodPost, "/jobs/nonexistent-job-id/nack", nil)
	nackReq.Header.Set("Authorization", "Bearer "+workerToken)
	nackRec := httptest.NewRecorder()
	router.ServeHTTP(nackRec, nackReq)

	if nackRec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d; body = %q", nackRec.Code, http.StatusBadRequest, nackRec.Body.String())
	}

	var errResp map[string]string
	if err := json.NewDecoder(nackRec.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if errResp["error"] == "" {
		t.Error("expected error message in response")
	}
}

func TestHandleNackJob_MissingJobID(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	_, workerToken := registerTestWorker(t, database)

	// An empty job ID in the path causes Go's ServeMux to clean the URL
	// and issue a 307 redirect. The handler is not reached.
	req := httptest.NewRequest(http.MethodPost, "/jobs//nack", nil)
	req.Header.Set("Authorization", "Bearer "+workerToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusTemporaryRedirect {
		t.Errorf("status = %d, want %d; body = %q", rec.Code, http.StatusTemporaryRedirect, rec.Body.String())
	}
}

func TestHandleNackJob_NoAuth(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	req := httptest.NewRequest(http.MethodPost, "/jobs/some-id/nack", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

// ---- Full Worker API Integration ----

func TestWorkerEndToEnd_ScheduleClaimAck(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	// Register worker.
	_, workerToken := registerTestWorker(t, database)

	// Schedule a job.
	jobBody := `{"payload":{"msg":"hello"}}`
	jobReq := httptest.NewRequest(http.MethodPost, "/queues/myqueue/jobs", strings.NewReader(jobBody))
	jobReq.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	jobRec := httptest.NewRecorder()
	router.ServeHTTP(jobRec, jobReq)
	if jobRec.Code != http.StatusCreated {
		t.Fatalf("schedule: status = %d", jobRec.Code)
	}

	// Claim.
	claimReq := httptest.NewRequest(http.MethodGet, "/queues/myqueue/next", nil)
	claimReq.Header.Set("Authorization", "Bearer "+workerToken)
	claimRec := httptest.NewRecorder()
	router.ServeHTTP(claimRec, claimReq)
	if claimRec.Code != http.StatusOK {
		t.Fatalf("claim: status = %d", claimRec.Code)
	}

	var claimResp map[string]interface{}
	if err := json.NewDecoder(claimRec.Body).Decode(&claimResp); err != nil {
		t.Fatalf("decode claim: %v", err)
	}
	jobID := claimResp["id"].(string)

	// Ack.
	ackReq := httptest.NewRequest(http.MethodPost, "/jobs/"+jobID+"/ack", nil)
	ackReq.Header.Set("Authorization", "Bearer "+workerToken)
	ackRec := httptest.NewRecorder()
	router.ServeHTTP(ackRec, ackReq)
	if ackRec.Code != http.StatusNoContent {
		t.Errorf("ack: status = %d, want %d", ackRec.Code, http.StatusNoContent)
	}

	// Queue should now be empty.
	claimReq2 := httptest.NewRequest(http.MethodGet, "/queues/myqueue/next", nil)
	claimReq2.Header.Set("Authorization", "Bearer "+workerToken)
	claimRec2 := httptest.NewRecorder()
	router.ServeHTTP(claimRec2, claimReq2)
	if claimRec2.Code != http.StatusNoContent {
		t.Errorf("expected empty queue after ack, got status = %d", claimRec2.Code)
	}
}

func TestWorkerEndToEnd_ScheduleClaimNackReclaimAck(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	// Register worker.
	_, workerToken := registerTestWorker(t, database)

	// Schedule.
	jobBody := `{"payload":{"n":"1"}}`
	jobReq := httptest.NewRequest(http.MethodPost, "/queues/wq/jobs", strings.NewReader(jobBody))
	jobReq.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	jobRec := httptest.NewRecorder()
	router.ServeHTTP(jobRec, jobReq)
	if jobRec.Code != http.StatusCreated {
		t.Fatalf("schedule: status = %d", jobRec.Code)
	}
	var jobResp map[string]interface{}
	if err := json.NewDecoder(jobRec.Body).Decode(&jobResp); err != nil {
		t.Fatalf("decode job: %v", err)
	}
	jobID := jobResp["id"].(string)

	// Claim.
	claimReq := httptest.NewRequest(http.MethodGet, "/queues/wq/next", nil)
	claimReq.Header.Set("Authorization", "Bearer "+workerToken)
	claimRec := httptest.NewRecorder()
	router.ServeHTTP(claimRec, claimReq)
	if claimRec.Code != http.StatusOK {
		t.Fatalf("claim: status = %d", claimRec.Code)
	}

	// Nack.
	nackReq := httptest.NewRequest(http.MethodPost, "/jobs/"+jobID+"/nack", nil)
	nackReq.Header.Set("Authorization", "Bearer "+workerToken)
	nackRec := httptest.NewRecorder()
	router.ServeHTTP(nackRec, nackReq)
	if nackRec.Code != http.StatusNoContent {
		t.Fatalf("nack: status = %d", nackRec.Code)
	}

	// Re-claim.
	claimReq2 := httptest.NewRequest(http.MethodGet, "/queues/wq/next", nil)
	claimReq2.Header.Set("Authorization", "Bearer "+workerToken)
	claimRec2 := httptest.NewRecorder()
	router.ServeHTTP(claimRec2, claimReq2)
	if claimRec2.Code != http.StatusOK {
		t.Fatalf("re-claim: status = %d", claimRec2.Code)
	}

	// Ack.
	ackReq := httptest.NewRequest(http.MethodPost, "/jobs/"+jobID+"/ack", nil)
	ackReq.Header.Set("Authorization", "Bearer "+workerToken)
	ackRec := httptest.NewRecorder()
	router.ServeHTTP(ackRec, ackReq)
	if ackRec.Code != http.StatusNoContent {
		t.Errorf("ack: status = %d, want %d", ackRec.Code, http.StatusNoContent)
	}
}

func TestWorkerEndToEnd_MultipleWorkersShareQueue(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	// Register two workers.
	_, token1 := registerTestWorker(t, database)
	_, token2 := registerTestWorker(t, database)

	// Schedule multiple jobs.
	for i := 0; i < 4; i++ {
		jobBody := `{"payload":{"idx":` + string(rune('0'+i)) + `}}`
		jobReq := httptest.NewRequest(http.MethodPost, "/queues/shared/jobs", strings.NewReader(jobBody))
		jobReq.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
		jobRec := httptest.NewRecorder()
		router.ServeHTTP(jobRec, jobReq)
		if jobRec.Code != http.StatusCreated {
			t.Fatalf("schedule job %d: status = %d", i, jobRec.Code)
		}
		var jobResp map[string]interface{}
		if err := json.NewDecoder(jobRec.Body).Decode(&jobResp); err != nil {
			t.Fatalf("decode job %d: %v", i, err)
		}
	}

	// Have each worker claim jobs and ack them.
	for i := 0; i < 4; i++ {
		token := token1
		if i%2 == 1 {
			token = token2
		}

		claimReq := httptest.NewRequest(http.MethodGet, "/queues/shared/next", nil)
		claimReq.Header.Set("Authorization", "Bearer "+token)
		claimRec := httptest.NewRecorder()
		router.ServeHTTP(claimRec, claimReq)
		if claimRec.Code != http.StatusOK {
			t.Fatalf("claim %d: status = %d", i, claimRec.Code)
		}

		var claimResp map[string]interface{}
		if err := json.NewDecoder(claimRec.Body).Decode(&claimResp); err != nil {
			t.Fatalf("decode claim %d: %v", i, err)
		}
		claimedID := claimResp["id"].(string)

		ackReq := httptest.NewRequest(http.MethodPost, "/jobs/"+claimedID+"/ack", nil)
		ackReq.Header.Set("Authorization", "Bearer "+token)
		ackRec := httptest.NewRecorder()
		router.ServeHTTP(ackRec, ackReq)
		if ackRec.Code != http.StatusNoContent {
			t.Errorf("ack %d: status = %d", i, ackRec.Code)
		}
	}

	// Queue should now be empty.
	claimReq := httptest.NewRequest(http.MethodGet, "/queues/shared/next", nil)
	claimReq.Header.Set("Authorization", "Bearer "+token1)
	claimRec := httptest.NewRecorder()
	router.ServeHTTP(claimRec, claimReq)
	if claimRec.Code != http.StatusNoContent {
		t.Errorf("expected empty queue, got status = %d", claimRec.Code)
	}
}
