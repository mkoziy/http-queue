package api

import (
	"encoding/json"
	"net/http"
	"time"

	badger "github.com/dgraph-io/badger/v4"

	"github.com/mkoziy/http-queue/config"
	"github.com/mkoziy/http-queue/queue"
)

const maxTTLSeconds = int64(1<<63-1) / int64(time.Second)

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

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MiB

	var body struct {
		Payload json.RawMessage `json:"payload"`
		TTL     *int64          `json:"ttl"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// If payload was not provided, use null.
	if body.Payload == nil {
		body.Payload = json.RawMessage("null")
	}

	var expiresAt *time.Time
	if body.TTL != nil {
		if *body.TTL <= 0 || *body.TTL > maxTTLSeconds {
			respondError(w, http.StatusBadRequest, "ttl must be between 1 and 9223372036 seconds or null")
			return
		}

		expiry := time.Now().UTC().Add(time.Duration(*body.TTL) * time.Second)
		expiresAt = &expiry
	}

	job, err := queue.ScheduleJobWithExpiry(h.db, queueName, body.Payload, expiresAt)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	respondJSON(w, http.StatusCreated, map[string]interface{}{
		"id":      job.ID,
		"queue":   job.Queue,
		"status":  job.Status,
		"created": job.CreatedAt,
		"ttl":     body.TTL,
	})
}
