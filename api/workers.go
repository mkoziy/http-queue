package api

import (
	"net/http"

	badger "github.com/dgraph-io/badger/v4"

	"github.com/mkoziy/http-queue/config"
	"github.com/mkoziy/http-queue/queue"
)

// WorkersHandler handles admin worker endpoints.
type WorkersHandler struct {
	db  *badger.DB
	cfg *config.Config
}

// NewWorkersHandler creates a new WorkersHandler.
func NewWorkersHandler(database *badger.DB, cfg *config.Config) *WorkersHandler {
	return &WorkersHandler{db: database, cfg: cfg}
}

// HandleRegisterWorker handles POST /workers
func (h *WorkersHandler) HandleRegisterWorker(w http.ResponseWriter, _ *http.Request) {
	id, plainToken, err := queue.RegisterWorker(h.db, h.cfg)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusCreated, map[string]string{
		"worker_id": id,
		"token":     plainToken,
	})
}

// HandleDeregisterWorker handles DELETE /workers/{id}
func (h *WorkersHandler) HandleDeregisterWorker(w http.ResponseWriter, r *http.Request) {
	workerID := pathParam(r.URL.Path, "/workers/", "")
	if workerID == "" {
		respondError(w, http.StatusBadRequest, "missing worker id")
		return
	}

	if err := queue.DeregisterWorker(h.db, workerID); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// HandleClaimNextJob handles GET /queues/{queue}/next
func (h *WorkersHandler) HandleClaimNextJob(w http.ResponseWriter, r *http.Request) {
	queueName := pathParam(r.URL.Path, "/queues/", "/next")
	if queueName == "" {
		respondError(w, http.StatusBadRequest, "missing queue name")
		return
	}

	worker := WorkerFromCtx(r.Context())
	if worker == nil {
		respondError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	job, err := queue.ClaimNextJob(h.db, queueName, worker.ID, h.cfg.VisibilityTimeout)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if job == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"id":       job.ID,
		"queue":    job.Queue,
		"payload":  job.Payload,
		"attempts": job.Attempts,
	})
}

// HandleAckJob handles POST /jobs/{id}/ack
func (h *WorkersHandler) HandleAckJob(w http.ResponseWriter, r *http.Request) {
	jobID := pathParam(r.URL.Path, "/jobs/", "/ack")
	if jobID == "" {
		respondError(w, http.StatusBadRequest, "missing job id")
		return
	}

	worker := WorkerFromCtx(r.Context())
	if worker == nil {
		respondError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	if err := queue.AckJob(h.db, jobID, worker.ID); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// HandleNackJob handles POST /jobs/{id}/nack
func (h *WorkersHandler) HandleNackJob(w http.ResponseWriter, r *http.Request) {
	jobID := pathParam(r.URL.Path, "/jobs/", "/nack")
	if jobID == "" {
		respondError(w, http.StatusBadRequest, "missing job id")
		return
	}

	worker := WorkerFromCtx(r.Context())
	if worker == nil {
		respondError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	if err := queue.NackJob(h.db, jobID, worker.ID, h.cfg.MaxAttempts); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
