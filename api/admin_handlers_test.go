//nolint:wrapcheck // test assertions pass through external errors; wrapping is noise here.
package api

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
		fmt.Sprintf(`{"payload":{"hello":"world"},"ttl":%d}`, int64(math.MaxInt64/int64(time.Second))+1),
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

	// PATCH instead of POST should not match the route.
	req := httptest.NewRequest(http.MethodPatch, "/queues/testqueue/jobs", nil)
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

// ---- HandleListWorkers Tests ----

func TestHandleListWorkers_Empty(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	req := httptest.NewRequest(http.MethodGet, "/workers", nil)
	req.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %q", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	items := resp["items"].([]interface{})
	if len(items) != 0 {
		t.Errorf("items len = %d, want 0", len(items))
	}
	if _, ok := resp["next_cursor"]; ok {
		t.Error("next_cursor should be absent on last page")
	}
}

func TestHandleListWorkers_ReturnsList(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	// Register three workers.
	var workerIDs []string
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/workers", nil)
		req.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("register worker %d: status = %d", i, rec.Code)
		}
		var resp map[string]string
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode register response: %v", err)
		}
		workerIDs = append(workerIDs, resp["worker_id"])
	}

	req := httptest.NewRequest(http.MethodGet, "/workers", nil)
	req.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %q", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	items := resp["items"].([]interface{})
	if len(items) != 3 {
		t.Errorf("items len = %d, want 3", len(items))
	}

	// Verify no token_hash in response.
	for _, item := range items {
		m := item.(map[string]interface{})
		if _, ok := m["tokenHash"]; ok {
			t.Error("response must not include tokenHash")
		}
		if m["id"] == nil || m["id"] == "" {
			t.Error("item missing id")
		}
		if m["registered_at"] == nil {
			t.Error("item missing registered_at")
		}
		if m["last_seen"] == nil {
			t.Error("item missing last_seen")
		}
	}
}

func TestHandleListWorkers_Pagination(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	// Register 5 workers.
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/workers", nil)
		req.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("register worker %d: status = %d", i, rec.Code)
		}
	}

	// Page 1: limit=2.
	req1 := httptest.NewRequest(http.MethodGet, "/workers?limit=2", nil)
	req1.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	rec1 := httptest.NewRecorder()
	router.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("page1 status = %d", rec1.Code)
	}
	var page1 map[string]interface{}
	if err := json.NewDecoder(rec1.Body).Decode(&page1); err != nil {
		t.Fatalf("decode page1: %v", err)
	}
	if len(page1["items"].([]interface{})) != 2 {
		t.Errorf("page1 items = %d, want 2", len(page1["items"].([]interface{})))
	}
	cursor, ok := page1["next_cursor"].(string)
	if !ok || cursor == "" {
		t.Fatal("page1 missing next_cursor")
	}

	// Page 2: limit=2, cursor from page 1.
	req2 := httptest.NewRequest(http.MethodGet, "/workers?limit=2&cursor="+cursor, nil)
	req2.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("page2 status = %d", rec2.Code)
	}
	var page2 map[string]interface{}
	if err := json.NewDecoder(rec2.Body).Decode(&page2); err != nil {
		t.Fatalf("decode page2: %v", err)
	}
	if len(page2["items"].([]interface{})) != 2 {
		t.Errorf("page2 items = %d, want 2", len(page2["items"].([]interface{})))
	}
	cursor2, ok := page2["next_cursor"].(string)
	if !ok || cursor2 == "" {
		t.Fatal("page2 missing next_cursor")
	}

	// Page 3: limit=2, cursor from page 2 — should have 1 item and no next_cursor.
	req3 := httptest.NewRequest(http.MethodGet, "/workers?limit=2&cursor="+cursor2, nil)
	req3.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	rec3 := httptest.NewRecorder()
	router.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("page3 status = %d", rec3.Code)
	}
	var page3 map[string]interface{}
	if err := json.NewDecoder(rec3.Body).Decode(&page3); err != nil {
		t.Fatalf("decode page3: %v", err)
	}
	if len(page3["items"].([]interface{})) != 1 {
		t.Errorf("page3 items = %d, want 1", len(page3["items"].([]interface{})))
	}
	if _, ok := page3["next_cursor"]; ok {
		t.Error("page3 should have no next_cursor (last page)")
	}
}

func TestHandleListWorkers_InvalidLimit(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	for _, qs := range []string{"?limit=0", "?limit=-1", "?limit=1001", "?limit=abc"} {
		req := httptest.NewRequest(http.MethodGet, "/workers"+qs, nil)
		req.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("qs=%q status = %d, want 400", qs, rec.Code)
		}
	}
}

func TestHandleListWorkers_NoAuth(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	req := httptest.NewRequest(http.MethodGet, "/workers", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

// ---- HandleListJobs Tests ----

func TestHandleListJobs_Empty(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	req := httptest.NewRequest(http.MethodGet, "/queues/testqueue/jobs", nil)
	req.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %q", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	items := resp["items"].([]interface{})
	if len(items) != 0 {
		t.Errorf("items len = %d, want 0", len(items))
	}
}

func TestHandleListJobs_DefaultStatusIsPending(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	// Schedule a job.
	body := `{"payload":{"key":"value"}}`
	req := httptest.NewRequest(http.MethodPost, "/queues/testqueue/jobs", strings.NewReader(body))
	req.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("schedule: status = %d", rec.Code)
	}

	// List without explicit status — defaults to pending.
	listReq := httptest.NewRequest(http.MethodGet, "/queues/testqueue/jobs", nil)
	listReq.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)

	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d", listRec.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(listRec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	items := resp["items"].([]interface{})
	if len(items) != 1 {
		t.Errorf("items len = %d, want 1", len(items))
	}
}

func TestHandleListJobs_StatusFilter(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	// Register a worker and schedule + claim a job to get it into reserved.
	_, workerToken := registerTestWorker(t, database)

	schedBody := `{"payload":{}}`
	schedReq := httptest.NewRequest(http.MethodPost, "/queues/testqueue/jobs", strings.NewReader(schedBody))
	schedReq.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	schedRec := httptest.NewRecorder()
	router.ServeHTTP(schedRec, schedReq)
	if schedRec.Code != http.StatusCreated {
		t.Fatalf("schedule status = %d", schedRec.Code)
	}

	claimReq := httptest.NewRequest(http.MethodGet, "/queues/testqueue/next", nil)
	claimReq.Header.Set("Authorization", "Bearer "+workerToken)
	claimRec := httptest.NewRecorder()
	router.ServeHTTP(claimRec, claimReq)
	if claimRec.Code != http.StatusOK {
		t.Fatalf("claim status = %d", claimRec.Code)
	}

	// pending list should be empty.
	pendReq := httptest.NewRequest(http.MethodGet, "/queues/testqueue/jobs?status=pending", nil)
	pendReq.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	pendRec := httptest.NewRecorder()
	router.ServeHTTP(pendRec, pendReq)
	var pendResp map[string]interface{}
	if err := json.NewDecoder(pendRec.Body).Decode(&pendResp); err != nil {
		t.Fatalf("decode pending: %v", err)
	}
	if len(pendResp["items"].([]interface{})) != 0 {
		t.Error("pending should be empty after claim")
	}

	// reserved list should have 1 job.
	resReq := httptest.NewRequest(http.MethodGet, "/queues/testqueue/jobs?status=reserved", nil)
	resReq.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	resRec := httptest.NewRecorder()
	router.ServeHTTP(resRec, resReq)
	var resResp map[string]interface{}
	if err := json.NewDecoder(resRec.Body).Decode(&resResp); err != nil {
		t.Fatalf("decode reserved: %v", err)
	}
	if len(resResp["items"].([]interface{})) != 1 {
		t.Errorf("reserved items = %d, want 1", len(resResp["items"].([]interface{})))
	}
}

func TestHandleListJobs_InvalidStatus(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	req := httptest.NewRequest(http.MethodGet, "/queues/testqueue/jobs?status=unknown", nil)
	req.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandleListJobs_Pagination(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	// Schedule 5 jobs.
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/queues/testqueue/jobs", strings.NewReader(`{"payload":{}}`))
		req.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("schedule job %d: status = %d", i, rec.Code)
		}
	}

	// Page 1: limit=2.
	req1 := httptest.NewRequest(http.MethodGet, "/queues/testqueue/jobs?status=pending&limit=2", nil)
	req1.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	rec1 := httptest.NewRecorder()
	router.ServeHTTP(rec1, req1)
	var page1 map[string]interface{}
	if err := json.NewDecoder(rec1.Body).Decode(&page1); err != nil {
		t.Fatalf("decode page1: %v", err)
	}
	if len(page1["items"].([]interface{})) != 2 {
		t.Errorf("page1 items = %d, want 2", len(page1["items"].([]interface{})))
	}
	cursor, ok := page1["next_cursor"].(string)
	if !ok || cursor == "" {
		t.Fatal("page1 missing next_cursor")
	}

	// Page 2: remaining 3 items with limit=4 — should return 3 and no next_cursor.
	req2 := httptest.NewRequest(http.MethodGet, "/queues/testqueue/jobs?status=pending&limit=4&cursor="+cursor, nil)
	req2.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)
	var page2 map[string]interface{}
	if err := json.NewDecoder(rec2.Body).Decode(&page2); err != nil {
		t.Fatalf("decode page2: %v", err)
	}
	if len(page2["items"].([]interface{})) != 3 {
		t.Errorf("page2 items = %d, want 3", len(page2["items"].([]interface{})))
	}
	if _, ok := page2["next_cursor"]; ok {
		t.Error("page2 should have no next_cursor (last page)")
	}
}

func TestHandleListJobs_InvalidQueueName(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	req := httptest.NewRequest(http.MethodGet, "/queues/bad:queue/jobs", nil)
	req.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandleListJobs_NoAuth(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	cfg := testConfig()
	router := New(database, cfg)

	req := httptest.NewRequest(http.MethodGet, "/queues/testqueue/jobs", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
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
