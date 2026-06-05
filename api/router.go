package api

import (
	"net/http"

	badger "github.com/dgraph-io/badger/v4"

	"github.com/mkoziy/http-queue/config"
)

// New builds the HTTP handler with all routes and middleware wired.
func New(database *badger.DB, cfg *config.Config) http.Handler {
	mux := http.NewServeMux()

	jobsHandler := NewJobsHandler(database, cfg)
	workersHandler := NewWorkersHandler(database, cfg)
	uiHandler := NewAdminUIHandler(database)

	// Admin routes (Basic Auth).
	adminAuth := BasicAuth(cfg)

	// POST /queues/{queue}/jobs
	mux.Handle("POST /queues/{queue}/jobs", adminAuth(http.HandlerFunc(jobsHandler.HandleScheduleJob)))

	// POST /workers
	mux.Handle("POST /workers", adminAuth(http.HandlerFunc(workersHandler.HandleRegisterWorker)))

	// GET /workers
	mux.Handle("GET /workers", adminAuth(http.HandlerFunc(workersHandler.HandleListWorkers)))

	// DELETE /workers/{id}
	mux.Handle("DELETE /workers/{id}", adminAuth(http.HandlerFunc(workersHandler.HandleDeregisterWorker)))

	// GET /queues/{queue}/jobs
	mux.Handle("GET /queues/{queue}/jobs", adminAuth(http.HandlerFunc(jobsHandler.HandleListJobs)))

	// Admin UI routes.
	mux.Handle("GET /admin/", adminAuth(http.HandlerFunc(uiHandler.HandleHome)))
	mux.Handle("GET /admin/queues/{queue}", adminAuth(http.HandlerFunc(uiHandler.HandleQueue)))

	// Worker routes (Bearer token).
	bearerAuth := BearerAuth(database, cfg.LastSeenDebounce)

	// GET /queues/{queue}/next
	mux.Handle("GET /queues/{queue}/next", bearerAuth(http.HandlerFunc(workersHandler.HandleClaimNextJob)))

	// POST /jobs/{id}/ack
	mux.Handle("POST /jobs/{id}/ack", bearerAuth(http.HandlerFunc(workersHandler.HandleAckJob)))

	// POST /jobs/{id}/nack
	mux.Handle("POST /jobs/{id}/nack", bearerAuth(http.HandlerFunc(workersHandler.HandleNackJob)))

	// Wrap with logging.
	return Logger(mux)
}


