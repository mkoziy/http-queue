package api

import (
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"net/url"

	badger "github.com/dgraph-io/badger/v4"

	"github.com/mkoziy/http-queue/queue"
)

//go:embed templates
var templatesFS embed.FS

// AdminUIHandler serves the read-only admin web UI.
type AdminUIHandler struct {
	db        *badger.DB
	homeTmpl  *template.Template
	queueTmpl *template.Template
}

// NewAdminUIHandler creates a new AdminUIHandler and parses templates.
func NewAdminUIHandler(database *badger.DB) *AdminUIHandler {
	homeTmpl := template.Must(template.ParseFS(templatesFS,
		"templates/base.html",
		"templates/home.html",
	))
	queueTmpl := template.Must(template.ParseFS(templatesFS,
		"templates/base.html",
		"templates/queue.html",
	))
	return &AdminUIHandler{
		db:        database,
		homeTmpl:  homeTmpl,
		queueTmpl: queueTmpl,
	}
}

// HandleHome handles GET /admin/
func (h *AdminUIHandler) HandleHome(w http.ResponseWriter, r *http.Request) {
	limit, cursor, ok := parsePaginationParams(w, r)
	if !ok {
		return
	}

	workers, nextCursor, err := queue.ListWorkers(h.db, cursor, limit)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	queues, err := queue.ListQueues(h.db)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	type workerRow struct {
		ID           string
		RegisteredAt string
		LastSeen     string
	}

	rows := make([]workerRow, len(workers))
	for i, wk := range workers {
		rows[i] = workerRow{
			ID:           wk.ID,
			RegisteredAt: wk.RegisteredAt.UTC().Format("2006-01-02 15:04:05"),
			LastSeen:     wk.LastSeen.UTC().Format("2006-01-02 15:04:05"),
		}
	}

	data := struct {
		Workers    []workerRow
		Queues     []string
		NextCursor string
	}{
		Workers:    rows,
		Queues:     queues,
		NextCursor: nextCursor,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.homeTmpl.ExecuteTemplate(w, "base", data); err != nil {
		// Headers already sent; log only.
		_ = fmt.Errorf("render home: %w", err)
	}
}

// HandleQueue handles GET /admin/queues/{queue}
func (h *AdminUIHandler) HandleQueue(w http.ResponseWriter, r *http.Request) {
	queueName := r.PathValue("queue")
	if queueName == "" {
		http.Error(w, "missing queue name", http.StatusBadRequest)
		return
	}

	statusStr := r.URL.Query().Get("status")
	if statusStr == "" {
		statusStr = string(queue.StatusPending)
	}

	var status queue.JobStatus
	switch queue.JobStatus(statusStr) {
	case queue.StatusPending, queue.StatusReserved, queue.StatusDead:
		status = queue.JobStatus(statusStr)
	default:
		http.Error(w, "invalid status", http.StatusBadRequest)
		return
	}

	limit, cursor, ok := parsePaginationParams(w, r)
	if !ok {
		return
	}

	jobs, nextCursor, err := queue.ListJobs(h.db, queueName, status, cursor, limit)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	pendingCount, _ := queue.CountJobs(h.db, queueName, queue.StatusPending)
	reservedCount, _ := queue.CountJobs(h.db, queueName, queue.StatusReserved)
	deadCount, _ := queue.CountJobs(h.db, queueName, queue.StatusDead)

	type jobRow struct {
		ID        string
		Attempts  int
		CreatedAt string
		ExpiresAt string
		WorkerID  string
	}

	rows := make([]jobRow, len(jobs))
	for i, j := range jobs {
		expiresAt := "—"
		if j.ExpiresAt != nil {
			expiresAt = j.ExpiresAt.UTC().Format("2006-01-02 15:04:05")
		}
		workerID := "—"
		if j.WorkerID != "" {
			workerID = j.WorkerID
		}
		rows[i] = jobRow{
			ID:        j.ID,
			Attempts:  j.Attempts,
			CreatedAt: j.CreatedAt.UTC().Format("2006-01-02 15:04:05"),
			ExpiresAt: expiresAt,
			WorkerID:  workerID,
		}
	}

	// Precompute next-page URL so the template doesn't need to concatenate query params.
	var nextURL string
	if nextCursor != "" {
		params := url.Values{"status": {statusStr}, "cursor": {nextCursor}}
		nextURL = "/admin/queues/" + url.PathEscape(queueName) + "?" + params.Encode()
	}

	data := struct {
		Queue         string
		Status        string
		Jobs          []jobRow
		NextURL       string
		PendingCount  int
		ReservedCount int
		DeadCount     int
	}{
		Queue:         queueName,
		Status:        statusStr,
		Jobs:          rows,
		NextURL:       nextURL,
		PendingCount:  pendingCount,
		ReservedCount: reservedCount,
		DeadCount:     deadCount,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.queueTmpl.ExecuteTemplate(w, "base", data); err != nil {
		_ = fmt.Errorf("render queue: %w", err)
	}
}
