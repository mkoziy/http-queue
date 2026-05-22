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
