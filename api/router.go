package api

import (
	"net/http"
	"strings"

	badger "github.com/dgraph-io/badger/v4"

	"github.com/mkoziy/http-queue/config"
)

// New builds the HTTP handler with all routes and middleware wired.
func New(database *badger.DB, cfg *config.Config) http.Handler {
	mux := http.NewServeMux()

	jobsHandler := NewJobsHandler(database, cfg)
	workersHandler := NewWorkersHandler(database, cfg)

	// Admin routes (Basic Auth).
	adminAuth := BasicAuth(cfg)

	// POST /queues/{queue}/jobs
	mux.Handle("POST /queues/{queue}/jobs", adminAuth(http.HandlerFunc(jobsHandler.HandleScheduleJob)))

	// POST /workers
	mux.Handle("POST /workers", adminAuth(http.HandlerFunc(workersHandler.HandleRegisterWorker)))

	// DELETE /workers/{id}
	mux.Handle("DELETE /workers/{id}", adminAuth(http.HandlerFunc(workersHandler.HandleDeregisterWorker)))

	// Worker routes (Bearer token).
	bearerAuth := BearerAuth(database, cfg.LastSeenDebounce)

	// GET /queues/{queue}/next
	mux.Handle("GET /queues/{queue}/next", bearerAuth(http.HandlerFunc(jobsHandler.HandleClaimNextJob)))

	// POST /jobs/{id}/ack
	mux.Handle("POST /jobs/{id}/ack", bearerAuth(http.HandlerFunc(jobsHandler.HandleAckJob)))

	// POST /jobs/{id}/nack
	mux.Handle("POST /jobs/{id}/nack", bearerAuth(http.HandlerFunc(jobsHandler.HandleNackJob)))

	// Wrap with logging.
	return Logger(mux)
}

// pathParam extracts the value of a path segment between a prefix and a suffix.
// For example, pathParam("/queues/myqueue/jobs", "/queues/", "/jobs") returns "myqueue".
// pathParam("/workers/myid", "/workers/", "") returns "myid".
func pathParam(path, prefix, suffix string) string {
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	trimmed := strings.TrimPrefix(path, prefix)
	if suffix != "" {
		idx := strings.Index(trimmed, suffix)
		if idx < 0 {
			return ""
		}
		return trimmed[:idx]
	}
	return trimmed
}
