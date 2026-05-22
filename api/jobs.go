package api

import (
	"encoding/json"
	"net/http"

	badger "github.com/dgraph-io/badger/v4"

	"github.com/mkoziy/http-queue/config"
	"github.com/mkoziy/http-queue/queue"
)

// JobsHandler handles admin job endpoints.
type JobsHandler struct {
	db  *badger.DB
	cfg *config.Config
}

// NewJobsHandler creates a new JobsHandler.
func NewJobsHandler(database *badger.DB, cfg *config.Config) *JobsHandler {
	return &JobsHandler{db: database, cfg: cfg}
}

// HandleScheduleJob handles POST /queues/{queue}/jobs
func (h *JobsHandler) HandleScheduleJob(w http.ResponseWriter, r *http.Request) {
	queueName := pathParam(r.URL.Path, "/queues/", "/jobs")
	if queueName == "" {
		respondError(w, http.StatusBadRequest, "missing queue name")
		return
	}

	var body struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// If payload was not provided, use null.
	if body.Payload == nil {
		body.Payload = json.RawMessage("null")
	}

	job, err := queue.ScheduleJob(h.db, queueName, body.Payload)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	respondJSON(w, http.StatusCreated, map[string]interface{}{
		"id":      job.ID,
		"queue":   job.Queue,
		"status":  job.Status,
		"created": job.CreatedAt,
	})
}

// HandleClaimNextJob handles GET /queues/{queue}/next
func (h *JobsHandler) HandleClaimNextJob(w http.ResponseWriter, r *http.Request) {
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
		"id":      job.ID,
		"queue":   job.Queue,
		"payload": job.Payload,
		"attempts": job.Attempts,
	})
}

// HandleAckJob handles POST /jobs/{id}/ack
func (h *JobsHandler) HandleAckJob(w http.ResponseWriter, r *http.Request) {
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
func (h *JobsHandler) HandleNackJob(w http.ResponseWriter, r *http.Request) {
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
