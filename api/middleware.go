// Package api provides the HTTP handlers and middleware for the queue engine.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	badger "github.com/dgraph-io/badger/v4"

	"github.com/mkoziy/http-queue/config"
	"github.com/mkoziy/http-queue/queue"
)

// contextKey is a private type for context keys to avoid collisions.
type contextKey string

const (
	ctxWorker = contextKey("worker")
)

// WorkerFromCtx extracts the worker from the request context.
func WorkerFromCtx(ctx context.Context) *queue.Worker {
	w, _ := ctx.Value(ctxWorker).(*queue.Worker)
	return w
}

// BasicAuth returns middleware that enforces HTTP Basic Authentication
// using the configured admin credentials.
func BasicAuth(cfg *config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, pass, ok := r.BasicAuth()
			if !ok {
				w.Header().Set("WWW-Authenticate", `Basic realm="http-queue admin"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			userMatch := subtle.ConstantTimeCompare([]byte(user), []byte(cfg.AdminUser)) == 1
			passMatch := subtle.ConstantTimeCompare([]byte(pass), []byte(cfg.AdminPass)) == 1

			if !userMatch || !passMatch {
				w.Header().Set("WWW-Authenticate", `Basic realm="http-queue admin"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// BearerAuth returns middleware that enforces Bearer token authentication
// for worker API endpoints. It looks up the worker by token and injects
// it into the request context. The worker's last-seen is also updated.
func BearerAuth(database *badger.DB, debounce time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" || len(authHeader) < 7 || authHeader[:7] != "Bearer " {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			plainToken := authHeader[7:]
			if plainToken == "" {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			worker, err := queue.WorkerByToken(database, plainToken)
			if err != nil {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			// Update last-seen (debounced).
			queue.TouchWorker(database, worker.ID, debounce)

			ctx := context.WithValue(r.Context(), ctxWorker, worker)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// Logger is a simple request logging middleware.
func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}

// respondJSON writes a JSON response with the given status code.
func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if data != nil {
		if err := json.NewEncoder(w).Encode(data); err != nil {
			log.Printf("json encode error: %v", err)
		}
	}
}

// respondError writes a JSON error response.
func respondError(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]string{"error": msg})
}

// parsePaginationParams extracts and validates ?limit and ?cursor query params.
// Returns (limit, cursor, ok); writes a 400 and returns ok=false on invalid input.
func parsePaginationParams(w http.ResponseWriter, r *http.Request) (int, string, bool) {
	limit := queue.DefaultListLimit
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > queue.MaxListLimit {
			respondError(w, http.StatusBadRequest, "limit must be between 1 and 1000")
			return 0, "", false
		}
		limit = n
	}
	cursor := r.URL.Query().Get("cursor")
	return limit, cursor, true
}
