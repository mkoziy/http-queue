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
	queueName := r.PathValue("queue")
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


