package api

import (
	"errors"
	"net/http"
	"strconv"

	badger "github.com/dgraph-io/badger/v4"

	"github.com/mkoziy/http-queue/config"
	"github.com/mkoziy/http-queue/queue"
)

// HandleListWorkers handles GET /workers
func (h *WorkersHandler) HandleListWorkers(w http.ResponseWriter, r *http.Request) {
	limit, cursor, ok := parsePaginationParams(w, r)
	if !ok {
		return
	}

	workers, nextCursor, err := queue.ListWorkers(h.db, cursor, limit)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type workerView struct {
		ID           string `json:"id"`
		RegisteredAt string `json:"registered_at"`
		LastSeen     string `json:"last_seen"`
	}

	items := make([]workerView, 0, len(workers))
	for _, wk := range workers {
		items = append(items, workerView{
			ID:           wk.ID,
			RegisteredAt: wk.RegisteredAt.UTC().Format("2006-01-02T15:04:05Z"),
			LastSeen:     wk.LastSeen.UTC().Format("2006-01-02T15:04:05Z"),
		})
	}

	resp := map[string]interface{}{"items": items}
	if nextCursor != "" {
		resp["next_cursor"] = nextCursor
	}
	respondJSON(w, http.StatusOK, resp)
}

const headerNextPollSeconds = "X-Next-Poll-Seconds"

// WorkersHandler handles admin worker endpoints.
type WorkersHandler struct {
	db              *badger.DB
	cfg             *config.Config
	nextPollAdvisor *queue.NextPollAdvisor
}

// NewWorkersHandler creates a new WorkersHandler.
func NewWorkersHandler(database *badger.DB, cfg *config.Config) *WorkersHandler {
	return &WorkersHandler{
		db:              database,
		cfg:             cfg,
		nextPollAdvisor: queue.NewNextPollAdvisor(cfg),
	}
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
	workerID := r.PathValue("id")
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
	queueName := r.PathValue("queue")
	if queueName == "" {
		respondError(w, http.StatusBadRequest, "missing queue name")
		return
	}

	worker := WorkerFromCtx(r.Context())
	if worker == nil {
		respondError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	nextPollSeconds, err := h.nextPollAdvisor.NextPollSeconds(queueName, worker.ID)
	if err != nil {
		if errors.Is(err, queue.ErrInvalidQueueName) {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set(headerNextPollSeconds, strconv.Itoa(nextPollSeconds))

	job, err := queue.ClaimNextJob(h.db, queueName, worker.ID, h.cfg.VisibilityTimeout)
	if err != nil {
		if errors.Is(err, queue.ErrInvalidQueueName) {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
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
	jobID := r.PathValue("id")
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
	jobID := r.PathValue("id")
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
