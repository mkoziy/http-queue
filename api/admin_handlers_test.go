//nolint:wrapcheck // test assertions pass through external errors; wrapping is noise here.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---- HandleScheduleJob Tests ----

func TestHandleScheduleJob_Success(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	body := `{"payload":{"hello":"world"}}`
	req := httptest.NewRequest(http.MethodPost, "/queues/testqueue/jobs", strings.NewReader(body))
	req.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d; body = %q", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if id, ok := resp["id"]; !ok || id == "" {
		t.Error("response missing or empty 'id'")
	}
	if resp["queue"] != "testqueue" {
		t.Errorf("queue = %v, want %q", resp["queue"], "testqueue")
	}
	if resp["status"] != "pending" {
		t.Errorf("status = %v, want %q", resp["status"], "pending")
	}
	if resp["created"] == nil || resp["created"] == "" {
		t.Error("response missing or empty 'created'")
	}
	if ttl, ok := resp["ttl"]; !ok || ttl != nil {
		t.Errorf("ttl = %v, want null", ttl)
	}
}

func TestHandleScheduleJob_NullPayload(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	// No payload field in the body — defaults to null.
	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/queues/testqueue/jobs", strings.NewReader(body))
	req.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d; body = %q", rec.Code, http.StatusCreated, rec.Body.String())
	}
}

func TestHandleScheduleJob_ExplicitNullPayload(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	body := `{"payload":null}`
	req := httptest.NewRequest(http.MethodPost, "/queues/testqueue/jobs", strings.NewReader(body))
	req.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d; body = %q", rec.Code, http.StatusCreated, rec.Body.String())
	}
}

func TestHandleScheduleJob_WithTTL(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	body := `{"payload":{"hello":"world"},"ttl":600}`
	req := httptest.NewRequest(http.MethodPost, "/queues/testqueue/jobs", strings.NewReader(body))
	req.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %q", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if ttl := resp["ttl"]; ttl != float64(600) {
		t.Errorf("ttl = %v, want 600", ttl)
	}
}

func TestHandleScheduleJob_NullTTL(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	body := `{"payload":{"hello":"world"},"ttl":null}`
	req := httptest.NewRequest(http.MethodPost, "/queues/testqueue/jobs", strings.NewReader(body))
	req.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %q", rec.Code, http.StatusCreated, rec.Body.String())
	}
}

func TestHandleScheduleJob_InvalidTTL(t *testing.T) {
	testCases := []string{
		`{"payload":{"hello":"world"},"ttl":0}`,
		`{"payload":{"hello":"world"},"ttl":-1}`,
		`{"payload":{"hello":"world"},"ttl":"600"}`,
	}

	for _, body := range testCases {
		t.Run(body, func(t *testing.T) {
			database, cleanup := openTestDB(t)
			defer cleanup()

			cfg := testConfig()
			router := New(database, cfg)

			req := httptest.NewRequest(http.MethodPost, "/queues/testqueue/jobs", strings.NewReader(body))
			req.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
			rec := httptest.NewRecorder()

			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body = %q", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
		})
	}
}

func TestHandleScheduleJob_EmptyBody(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	req := httptest.NewRequest(http.MethodPost, "/queues/testqueue/jobs", http.NoBody)
	req.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	// Empty body should fail JSON decode.
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d; body = %q", rec.Code, http.StatusBadRequest, rec.Body.String())
	}

	var errResp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if errResp["error"] == "" {
		t.Error("expected error message in response")
	}
}

func TestHandleScheduleJob_InvalidBody(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	body := `{invalid json}`
	req := httptest.NewRequest(http.MethodPost, "/queues/testqueue/jobs", strings.NewReader(body))
	req.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d; body = %q", rec.Code, http.StatusBadRequest, rec.Body.String())
	}

	var errResp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if errResp["error"] == "" {
		t.Error("expected error message in response")
	}
}

func TestHandleScheduleJob_PayloadTooLarge(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	// Build a payload that exceeds 1 MiB.
	largePayload := make([]byte, (1<<20)+1)
	for i := range largePayload {
		largePayload[i] = 'x'
	}
	body := fmt.Sprintf(`{"payload":"%s"}`, string(largePayload))
	req := httptest.NewRequest(http.MethodPost, "/queues/testqueue/jobs", strings.NewReader(body))
	req.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d; body = %q", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestHandleScheduleJob_QueueNameWithColon(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	body := `{"payload":{}}`
	req := httptest.NewRequest(http.MethodPost, "/queues/bad:queue/jobs", strings.NewReader(body))
	req.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d; body = %q", rec.Code, http.StatusBadRequest, rec.Body.String())
	}

	var errResp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if errResp["error"] == "" {
		t.Error("expected error message in response")
	}
}

func TestHandleScheduleJob_NoAuth(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	body := `{"payload":{}}`
	req := httptest.NewRequest(http.MethodPost, "/queues/testqueue/jobs", strings.NewReader(body))
	// No auth header.
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestHandleScheduleJob_WrongAuth(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	body := `{"payload":{}}`
	req := httptest.NewRequest(http.MethodPost, "/queues/testqueue/jobs", strings.NewReader(body))
	req.SetBasicAuth("wronguser", "wrongpass")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestHandleScheduleJob_WrongMethod(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	// GET instead of POST should not match the route.
	req := httptest.NewRequest(http.MethodGet, "/queues/testqueue/jobs", nil)
	req.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	// Go 1.22+ ServeMux returns 405 Method Not Allowed for method mismatches.
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleScheduleJob_ContentType(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	body := `{"payload":{"hello":"world"}}`
	req := httptest.NewRequest(http.MethodPost, "/queues/testqueue/jobs", strings.NewReader(body))
	req.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

func TestHandleScheduleJob_MultipleQueues(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	queues := []string{"queue-a", "queue-b", "queue-c"}
	for _, q := range queues {
		body := `{"payload":{"queue":"` + q + `"}}`
		req := httptest.NewRequest(http.MethodPost, "/queues/"+q+"/jobs", strings.NewReader(body))
		req.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusCreated {
			t.Errorf("queue %q: status = %d, want %d; body = %q", q, rec.Code, http.StatusCreated, rec.Body.String())
			continue
		}

		var resp map[string]interface{}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("queue %q: decode response: %v", q, err)
		}
		if resp["queue"] != q {
			t.Errorf("queue %q: response queue = %v, want %q", q, resp["queue"], q)
		}
	}
}

// ---- HandleRegisterWorker Tests ----

func TestHandleRegisterWorker_Success(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	req := httptest.NewRequest(http.MethodPost, "/workers", strings.NewReader(`{}`))
	req.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d; body = %q", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp["worker_id"] == "" {
		t.Error("response missing or empty 'worker_id'")
	}
	if resp["token"] == "" {
		t.Error("response missing or empty 'token'")
	}
}

func TestHandleRegisterWorker_ReturnsUniqueTokens(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	tokens := make(map[string]bool)
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/workers", strings.NewReader(`{}`))
		req.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusCreated {
			t.Fatalf("iteration %d: status = %d, want %d", i, rec.Code, http.StatusCreated)
		}

		var resp map[string]string
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("iteration %d: decode response: %v", i, err)
		}

		if tokens[resp["token"]] {
			t.Errorf("iteration %d: duplicate token %q", i, resp["token"])
		}
		tokens[resp["token"]] = true
	}
}

func TestHandleRegisterWorker_NoAuth(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	req := httptest.NewRequest(http.MethodPost, "/workers", nil)
	// No auth header.
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestHandleRegisterWorker_WrongAuth(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	req := httptest.NewRequest(http.MethodPost, "/workers", nil)
	req.SetBasicAuth("wronguser", "wrongpass")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestHandleRegisterWorker_WrongMethod(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	// DELETE instead of POST should not match.
	req := httptest.NewRequest(http.MethodDelete, "/workers", nil)
	req.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	// Go 1.22+ ServeMux returns 405 for method mismatches.
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleRegisterWorker_ContentType(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	req := httptest.NewRequest(http.MethodPost, "/workers", nil)
	req.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

// ---- HandleDeregisterWorker Tests ----

func TestHandleDeregisterWorker_Success(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	// First register a worker.
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
	workerID := regResp["worker_id"]

	// Deregister the worker.
	delReq := httptest.NewRequest(http.MethodDelete, "/workers/"+workerID, nil)
	delReq.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	delRec := httptest.NewRecorder()

	router.ServeHTTP(delRec, delReq)

	if delRec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d; body = %q", delRec.Code, http.StatusNoContent, delRec.Body.String())
	}
}

func TestHandleDeregisterWorker_Nonexistent(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	req := httptest.NewRequest(http.MethodDelete, "/workers/nonexistent-worker-id", nil)
	req.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d; body = %q", rec.Code, http.StatusBadRequest, rec.Body.String())
	}

	var errResp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if errResp["error"] == "" {
		t.Error("expected error message in response")
	}
}

func TestHandleDeregisterWorker_DoubleDeregister(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	// Register a worker.
	regReq := httptest.NewRequest(http.MethodPost, "/workers", nil)
	regReq.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	regRec := httptest.NewRecorder()
	router.ServeHTTP(regRec, regReq)

	var regResp map[string]string
	if err := json.NewDecoder(regRec.Body).Decode(&regResp); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	workerID := regResp["worker_id"]

	// First deregister.
	delReq1 := httptest.NewRequest(http.MethodDelete, "/workers/"+workerID, nil)
	delReq1.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	delRec1 := httptest.NewRecorder()
	router.ServeHTTP(delRec1, delReq1)

	if delRec1.Code != http.StatusNoContent {
		t.Fatalf("first deregister: status = %d, want %d", delRec1.Code, http.StatusNoContent)
	}

	// Second deregister should fail.
	delReq2 := httptest.NewRequest(http.MethodDelete, "/workers/"+workerID, nil)
	delReq2.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	delRec2 := httptest.NewRecorder()
	router.ServeHTTP(delRec2, delReq2)

	if delRec2.Code != http.StatusBadRequest {
		t.Errorf("second deregister: status = %d, want %d; body = %q", delRec2.Code, http.StatusBadRequest, delRec2.Body.String())
	}
}

func TestHandleDeregisterWorker_NoAuth(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	req := httptest.NewRequest(http.MethodDelete, "/workers/some-id", nil)
	// No auth header.
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestHandleDeregisterWorker_WrongAuth(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	req := httptest.NewRequest(http.MethodDelete, "/workers/some-id", nil)
	req.SetBasicAuth("wronguser", "wrongpass")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestHandleDeregisterWorker_WrongMethod(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	// POST instead of DELETE should not match.
	req := httptest.NewRequest(http.MethodPost, "/workers/some-id", nil)
	req.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleDeregisterWorker_RequeuesReservedJobs(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	// Register a worker.
	regReq := httptest.NewRequest(http.MethodPost, "/workers", nil)
	regReq.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	regRec := httptest.NewRecorder()
	router.ServeHTTP(regRec, regReq)

	var regResp map[string]string
	if err := json.NewDecoder(regRec.Body).Decode(&regResp); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	workerID := regResp["worker_id"]
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

	// Claim the job as the worker (using worker Bearer token).
	claimReq := httptest.NewRequest(http.MethodGet, "/queues/testqueue/next", nil)
	claimReq.Header.Set("Authorization", "Bearer "+workerToken)
	claimRec := httptest.NewRecorder()
	router.ServeHTTP(claimRec, claimReq)

	if claimRec.Code != http.StatusOK {
		t.Fatalf("claim job: status = %d, want %d", claimRec.Code, http.StatusOK)
	}

	// Deregister the worker — this should re-queue the reserved job.
	delReq := httptest.NewRequest(http.MethodDelete, "/workers/"+workerID, nil)
	delReq.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	delRec := httptest.NewRecorder()
	router.ServeHTTP(delRec, delReq)

	if delRec.Code != http.StatusNoContent {
		t.Fatalf("deregister: status = %d, want %d", delRec.Code, http.StatusNoContent)
	}

	// The job should now be claimable by another worker (re-queued).
	// Register a new worker to claim it.
	regReq2 := httptest.NewRequest(http.MethodPost, "/workers", nil)
	regReq2.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	regRec2 := httptest.NewRecorder()
	router.ServeHTTP(regRec2, regReq2)

	var regResp2 map[string]string
	if err := json.NewDecoder(regRec2.Body).Decode(&regResp2); err != nil {
		t.Fatalf("decode second register response: %v", err)
	}
	newToken := regResp2["token"]

	// Claim the job with the new worker.
	claimReq2 := httptest.NewRequest(http.MethodGet, "/queues/testqueue/next", nil)
	claimReq2.Header.Set("Authorization", "Bearer "+newToken)
	claimRec2 := httptest.NewRecorder()
	router.ServeHTTP(claimRec2, claimReq2)

	if claimRec2.Code != http.StatusOK {
		t.Errorf("re-claim after deregister: status = %d, want %d; body = %q", claimRec2.Code, http.StatusOK, claimRec2.Body.String())
	} else {
		var reClaimed map[string]interface{}
		if err := json.NewDecoder(claimRec2.Body).Decode(&reClaimed); err != nil {
			t.Fatalf("decode re-claimed response: %v", err)
		}
		if reClaimed["id"] != jobID {
			t.Errorf("re-claimed job ID = %v, want %q", reClaimed["id"], jobID)
		}
	}
}

// ---- Full Admin Integration ----

func TestAdminEndToEnd_ScheduleAndDeregister(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	// Register two workers.
	workerIDs := make([]string, 2)
	for i := 0; i < 2; i++ {
		regReq := httptest.NewRequest(http.MethodPost, "/workers", nil)
		regReq.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
		regRec := httptest.NewRecorder()
		router.ServeHTTP(regRec, regReq)
		if regRec.Code != http.StatusCreated {
			t.Fatalf("register worker %d: status = %d", i, regRec.Code)
		}
		var regResp map[string]string
		if err := json.NewDecoder(regRec.Body).Decode(&regResp); err != nil {
			t.Fatalf("decode register %d: %v", i, err)
		}
		workerIDs[i] = regResp["worker_id"]
	}

	// Deregister the first worker.
	delReq := httptest.NewRequest(http.MethodDelete, "/workers/"+workerIDs[0], nil)
	delReq.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	delRec := httptest.NewRecorder()
	router.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusNoContent {
		t.Errorf("deregister worker: status = %d, want %d", delRec.Code, http.StatusNoContent)
	}

	// Verify the second worker is still registered.
	// (We can't easily verify this through the API without a "list workers" endpoint,
	// but we can verify it by trying to deregister it and getting success.)
	delReq2 := httptest.NewRequest(http.MethodDelete, "/workers/"+workerIDs[1], nil)
	delReq2.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	delRec2 := httptest.NewRecorder()
	router.ServeHTTP(delRec2, delReq2)
	if delRec2.Code != http.StatusNoContent {
		t.Errorf("deregister second worker: status = %d, want %d", delRec2.Code, http.StatusNoContent)
	}
}
