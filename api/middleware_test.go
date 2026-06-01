//nolint:wrapcheck // test assertions pass through external errors; wrapping is noise here.
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	badger "github.com/dgraph-io/badger/v4"

	"github.com/mkoziy/http-queue/config"
	"github.com/mkoziy/http-queue/db"
	"github.com/mkoziy/http-queue/queue"
)

// ---- Test helpers ----

// testConfig returns a minimal config.Config with test-appropriate values.
func testConfig() *config.Config {
	return &config.Config{
		AdminUser:                "admin",
		AdminPass:                "secret",
		VisibilityTimeout:        30 * time.Second,
		MaxAttempts:              3,
		LastSeenDebounce:         100 * time.Millisecond,
		SweepInterval:            10 * time.Minute, // don't trigger sweeps during tests
		WorkerExpiry:             10 * time.Minute,
		WorkerNextBaseInterval:   5 * time.Second,
		WorkerNextMinInterval:    1 * time.Second,
		WorkerNextMaxInterval:    1 * time.Minute,
		WorkerNextActivityWindow: 1 * time.Minute,
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

// okHandler returns an http.Handler that writes "ok" with 200 OK.
func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

// registerTestWorker registers a worker and returns its ID and plaintext token.
func registerTestWorker(t *testing.T, database *badger.DB) (id, plainToken string) {
	t.Helper()

	cfg := testConfig()
	id, plainToken, err := queue.RegisterWorker(database, cfg)
	if err != nil {
		t.Fatalf("RegisterWorker() error: %v", err)
	}
	if id == "" {
		t.Fatal("RegisterWorker() returned empty worker ID")
	}
	if plainToken == "" {
		t.Fatal("RegisterWorker() returned empty token")
	}
	return id, plainToken
}

// ---- BasicAuth Tests ----

func TestBasicAuth_ValidCredentials(t *testing.T) {
	cfg := testConfig()
	middleware := BasicAuth(cfg)
	handler := middleware(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth(cfg.AdminUser, cfg.AdminPass)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "ok" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "ok")
	}
}

func TestBasicAuth_MissingHeader(t *testing.T) {
	cfg := testConfig()
	middleware := BasicAuth(cfg)
	handler := middleware(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestBasicAuth_WrongUser(t *testing.T) {
	cfg := testConfig()
	middleware := BasicAuth(cfg)
	handler := middleware(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("wronguser", cfg.AdminPass)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	// Must not leak which component was wrong.
	if rec.Body.String() != "Unauthorized\n" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "Unauthorized\n")
	}
}

func TestBasicAuth_WrongPassword(t *testing.T) {
	cfg := testConfig()
	middleware := BasicAuth(cfg)
	handler := middleware(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth(cfg.AdminUser, "wrongpass")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if rec.Body.String() != "Unauthorized\n" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "Unauthorized\n")
	}
}

func TestBasicAuth_BothWrong(t *testing.T) {
	cfg := testConfig()
	middleware := BasicAuth(cfg)
	handler := middleware(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("wronguser", "wrongpass")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestBasicAuth_InvalidEncoding(t *testing.T) {
	// An Authorization header with invalid base64 causes r.BasicAuth() to return ok=false.
	cfg := testConfig()
	middleware := BasicAuth(cfg)
	handler := middleware(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Basic this-is-not-valid-base64!!!")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestBasicAuth_EmptyCredentials(t *testing.T) {
	// The Basic auth header with ":" (empty user and pass) should still be rejected.
	cfg := testConfig()
	middleware := BasicAuth(cfg)
	handler := middleware(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("", "")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestBasicAuth_WrongScheme(t *testing.T) {
	// Using Bearer instead of Basic should be rejected.
	cfg := testConfig()
	middleware := BasicAuth(cfg)
	handler := middleware(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer sometoken")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestBasicAuth_ResponseDoesNotLeakInfo(t *testing.T) {
	// Verify that error responses for wrong user vs wrong pass are identical.
	cfg := testConfig()

	// Wrong user.
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.SetBasicAuth("wronguser", cfg.AdminPass)
	rec1 := httptest.NewRecorder()
	BasicAuth(cfg)(okHandler()).ServeHTTP(rec1, req1)

	// Wrong pass.
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.SetBasicAuth(cfg.AdminUser, "wrongpass")
	rec2 := httptest.NewRecorder()
	BasicAuth(cfg)(okHandler()).ServeHTTP(rec2, req2)

	if rec1.Code != rec2.Code {
		t.Error("status codes differ between wrong-user and wrong-pass responses")
	}
	if rec1.Body.String() != rec2.Body.String() {
		t.Error("body differs between wrong-user and wrong-pass responses")
	}
	if rec1.Header().Get("Content-Type") != rec2.Header().Get("Content-Type") {
		t.Error("content-type differs between wrong-user and wrong-pass responses")
	}
}

// ---- BearerAuth Tests ----

func TestBearerAuth_ValidToken(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	_, plainToken := registerTestWorker(t, database)

	middleware := BearerAuth(database, testConfig().LastSeenDebounce)
	handler := middleware(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+plainToken)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "ok" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "ok")
	}
}

func TestBearerAuth_ValidTokenInjectsWorkerContext(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	workerID, plainToken := registerTestWorker(t, database)

	// Handler that checks the worker from context.
	checkHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		worker := WorkerFromCtx(r.Context())
		if worker == nil {
			http.Error(w, "no worker in context", http.StatusInternalServerError)
			return
		}
		if worker.ID != workerID {
			http.Error(w, "wrong worker ID", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	middleware := BearerAuth(database, testConfig().LastSeenDebounce)
	handler := middleware(checkHandler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+plainToken)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body = %q", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestBearerAuth_MissingHeader(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	middleware := BearerAuth(database, testConfig().LastSeenDebounce)
	handler := middleware(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestBearerAuth_WrongScheme(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	_, plainToken := registerTestWorker(t, database)

	middleware := BearerAuth(database, testConfig().LastSeenDebounce)
	handler := middleware(okHandler())

	// Using Basic auth scheme instead of Bearer.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("user", plainToken)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestBearerAuth_InvalidToken(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	middleware := BearerAuth(database, testConfig().LastSeenDebounce)
	handler := middleware(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer some-invalid-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestBearerAuth_EmptyToken(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	middleware := BearerAuth(database, testConfig().LastSeenDebounce)
	handler := middleware(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer ")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestBearerAuth_MalformedHeader(t *testing.T) {
	// Authorization header with "Bearer" but no space or token.
	database, cleanup := openTestDB(t)
	defer cleanup()

	middleware := BearerAuth(database, testConfig().LastSeenDebounce)
	handler := middleware(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "BearerExtra")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestBearerAuth_ExpiredWorkerDeleted(t *testing.T) {
	// If a worker is deregistered, its token should no longer work.
	database, cleanup := openTestDB(t)
	defer cleanup()

	workerID, plainToken := registerTestWorker(t, database)

	// Deregister the worker.
	if err := queue.DeregisterWorker(database, workerID); err != nil {
		t.Fatalf("DeregisterWorker() error: %v", err)
	}

	middleware := BearerAuth(database, testConfig().LastSeenDebounce)
	handler := middleware(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+plainToken)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d (deregistered worker token should be rejected)", rec.Code, http.StatusUnauthorized)
	}
}

func TestBearerAuth_ResponseBodyDoesNotLeakInfo(t *testing.T) {
	database, cleanup := openTestDB(t)
	defer cleanup()

	middleware := BearerAuth(database, testConfig().LastSeenDebounce)

	// Send requests with various invalid token scenarios and verify identical responses.
	var responses []*httptest.ResponseRecorder

	// Missing header.
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	rec1 := httptest.NewRecorder()
	middleware(okHandler()).ServeHTTP(rec1, req1)
	responses = append(responses, rec1)

	// Invalid token.
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.Header.Set("Authorization", "Bearer invalidtoken123")
	rec2 := httptest.NewRecorder()
	middleware(okHandler()).ServeHTTP(rec2, req2)
	responses = append(responses, rec2)

	// Wrong scheme.
	req3 := httptest.NewRequest(http.MethodGet, "/", nil)
	req3.SetBasicAuth("user", "pass")
	rec3 := httptest.NewRecorder()
	middleware(okHandler()).ServeHTTP(rec3, req3)
	responses = append(responses, rec3)

	// Empty bearer token.
	req4 := httptest.NewRequest(http.MethodGet, "/", nil)
	req4.Header.Set("Authorization", "Bearer ")
	rec4 := httptest.NewRecorder()
	middleware(okHandler()).ServeHTTP(rec4, req4)
	responses = append(responses, rec4)

	for i := 1; i < len(responses); i++ {
		if responses[i].Code != responses[0].Code {
			t.Errorf("response %d status = %d, want %d (matching first)", i, responses[i].Code, responses[0].Code)
		}
		if responses[i].Body.String() != responses[0].Body.String() {
			t.Errorf("response %d body = %q, want %q", i, responses[i].Body.String(), responses[0].Body.String())
		}
	}
}

// ---- WorkerFromCtx Tests ----

func TestWorkerFromCtx_ReturnsWorker(t *testing.T) {
	w := &queue.Worker{ID: "test-worker-id"}
	ctx := context.WithValue(context.Background(), ctxWorker, w)

	got := WorkerFromCtx(ctx)
	if got == nil {
		t.Fatal("WorkerFromCtx() returned nil")
	}
	if got.ID != w.ID {
		t.Errorf("WorkerFromCtx().ID = %q, want %q", got.ID, w.ID)
	}
}

func TestWorkerFromCtx_ReturnsNil(t *testing.T) {
	got := WorkerFromCtx(context.Background())
	if got != nil {
		t.Errorf("WorkerFromCtx() = %v, want nil", got)
	}
}

func TestWorkerFromCtx_WrongType(t *testing.T) {
	ctx := context.WithValue(context.Background(), ctxWorker, "not-a-worker")
	got := WorkerFromCtx(ctx)
	if got != nil {
		t.Errorf("WorkerFromCtx() = %v, want nil for wrong type", got)
	}
}

func TestWorkerFromCtx_DifferentContextKey(t *testing.T) {
	// A different context key should not be confused with ctxWorker.
	type otherKey string
	ctx := context.WithValue(context.Background(), otherKey("worker"), &queue.Worker{ID: "other"})
	got := WorkerFromCtx(ctx)
	if got != nil {
		t.Errorf("WorkerFromCtx() = %v, want nil for different key", got)
	}
}

// ---- respondJSON Tests ----

func TestRespondJSON_SetsContentType(t *testing.T) {
	rec := httptest.NewRecorder()
	respondJSON(rec, http.StatusOK, map[string]string{"msg": "hello"})

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

func TestRespondJSON_WritesStatusCode(t *testing.T) {
	rec := httptest.NewRecorder()
	respondJSON(rec, http.StatusCreated, nil)

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusCreated)
	}
}

func TestRespondJSON_WritesBody(t *testing.T) {
	rec := httptest.NewRecorder()
	respondJSON(rec, http.StatusOK, map[string]string{"key": "value"})

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	if body["key"] != "value" {
		t.Errorf("body[key] = %q, want %q", body["key"], "value")
	}
}

func TestRespondJSON_NilBody(t *testing.T) {
	rec := httptest.NewRecorder()
	respondJSON(rec, http.StatusNoContent, nil)

	// With nil data, the body should be empty (or nil written).
	if rec.Body.Len() != 0 {
		t.Errorf("body length = %d, want 0 for nil data", rec.Body.Len())
	}
}

func TestRespondJSON_EmptyMap(t *testing.T) {
	rec := httptest.NewRecorder()
	respondJSON(rec, http.StatusOK, map[string]string{})

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	if len(body) != 0 {
		t.Errorf("body length = %d, want 0", len(body))
	}
}

func TestRespondJSON_ErrorStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	respondJSON(rec, http.StatusBadRequest, map[string]string{"error": "bad request"})

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// ---- respondError Tests ----

func TestRespondError_WritesErrorJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	respondError(rec, http.StatusBadRequest, "something went wrong")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	if body["error"] != "something went wrong" {
		t.Errorf(`body["error"] = %q, want %q`, body["error"], "something went wrong")
	}
}

func TestRespondError_InternalServerError(t *testing.T) {
	rec := httptest.NewRecorder()
	respondError(rec, http.StatusInternalServerError, "internal error")

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	if body["error"] != "internal error" {
		t.Errorf(`body["error"] = %q, want %q`, body["error"], "internal error")
	}
}
