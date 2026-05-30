package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// isTransient returns true for connection errors that are expected during server restarts.
func isTransient(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	// connection refused, EOF on keep-alive, etc.
	var opErr *net.OpError
	return errors.As(err, &opErr)
}

// logRT is a logging RoundTripper that records each HTTP exchange as a structured event.
type logRT struct {
	base  http.RoundTripper
	log   *slog.Logger
	stats *counters
	ev    *eventWriter
}

func (l *logRT) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := l.base.RoundTrip(req)
	dur := time.Since(start)

	status := 0
	if resp != nil {
		status = resp.StatusCode
	}

	if err != nil {
		if isTransient(err) {
			l.log.Debug("transient http error (expected during restart)",
				"method", req.Method,
				"path", req.URL.Path,
				"duration_ms", dur.Milliseconds(),
				"err", err.Error(),
			)
		} else {
			l.log.Warn("http request error",
				"method", req.Method,
				"path", req.URL.Path,
				"duration_ms", dur.Milliseconds(),
				"err", err.Error(),
			)
		}
		l.stats.httpErrors.Add(1)
		l.ev.Write("warn", "http", "http_error", map[string]any{
			"method":      req.Method,
			"path":        req.URL.Path,
			"duration_ms": dur.Milliseconds(),
			"err":         err.Error(),
			"ok":          false,
		})
		return resp, err
	}

	l.log.Debug("http",
		"method", req.Method,
		"path", req.URL.Path,
		"status", status,
		"duration_ms", dur.Milliseconds(),
	)
	l.ev.Write("debug", "http", "http_exchange", map[string]any{
		"method":      req.Method,
		"path":        req.URL.Path,
		"status":      status,
		"duration_ms": dur.Milliseconds(),
	})
	return resp, nil
}

// newClient returns an http.Client instrumented with logRT.
func newClient(log *slog.Logger, stats *counters, events *eventWriter) *http.Client {
	base := &http.Transport{
		DisableKeepAlives: true,
		DialContext: (&net.Dialer{
			Timeout: 5 * time.Second,
		}).DialContext,
	}
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &logRT{
			base:  base,
			log:   log,
			stats: stats,
			ev:    events,
		},
	}
}

// apiClient wraps an http.Client with the server base URL and credentials.
type apiClient struct {
	hc        *http.Client
	baseURL   string
	adminUser string
	adminPass string
	log       *slog.Logger
	stats     *counters
	events    *eventWriter
}

// RegisterWorkerResp is the decoded body of POST /workers.
type RegisterWorkerResp struct {
	WorkerID string `json:"worker_id"`
	Token    string `json:"token"`
}

// PublishJobResp is the decoded body of POST /queues/{queue}/jobs.
type PublishJobResp struct {
	ID    string `json:"id"`
	Queue string `json:"queue"`
}

// ClaimResp is the decoded body of GET /queues/{queue}/next.
type ClaimResp struct {
	ID       string          `json:"id"`
	Queue    string          `json:"queue"`
	Payload  json.RawMessage `json:"payload"`
	Attempts int             `json:"attempts"`
}

// doJSON executes req and JSON-decodes a 2xx response body into dst (may be nil).
// Returns (statusCode, err). On non-2xx, err is nil but status is set.
func (c *apiClient) doJSON(ctx context.Context, req *http.Request, dst any) (int, error) {
	req = req.WithContext(ctx)
	resp, err := c.hc.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && dst != nil {
		if err := json.Unmarshal(body, dst); err != nil {
			return resp.StatusCode, fmt.Errorf("decode response: %w (body: %s)", err, body)
		}
	}
	return resp.StatusCode, nil
}

// RegisterWorker calls POST /workers with admin Basic Auth.
func (c *apiClient) RegisterWorker(ctx context.Context) (*RegisterWorkerResp, error) {
	req, _ := http.NewRequest(http.MethodPost, c.baseURL+"/workers", nil)
	req.SetBasicAuth(c.adminUser, c.adminPass)
	var out RegisterWorkerResp
	status, err := c.doJSON(ctx, req, &out)
	if err != nil {
		return nil, err
	}
	if status != http.StatusCreated {
		return nil, fmt.Errorf("register worker: unexpected status %d", status)
	}
	return &out, nil
}

// DeregisterWorker calls DELETE /workers/{id} with admin Basic Auth.
func (c *apiClient) DeregisterWorker(ctx context.Context, workerID string) (int, error) {
	req, _ := http.NewRequest(http.MethodDelete, c.baseURL+"/workers/"+workerID, nil)
	req.SetBasicAuth(c.adminUser, c.adminPass)
	resp, err := c.hc.Do(req.WithContext(ctx))
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}

// PublishJob calls POST /queues/{queue}/jobs with admin Basic Auth.
func (c *apiClient) PublishJob(ctx context.Context, queue string, payload any) (*PublishJobResp, error) {
	body, err := json.Marshal(map[string]any{"payload": payload})
	if err != nil {
		return nil, err
	}
	req, _ := http.NewRequest(http.MethodPost, c.baseURL+"/queues/"+queue+"/jobs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.adminUser, c.adminPass)
	var out PublishJobResp
	status, err := c.doJSON(ctx, req, &out)
	if err != nil {
		return nil, err
	}
	if status != http.StatusCreated {
		return nil, fmt.Errorf("publish job: unexpected status %d", status)
	}
	return &out, nil
}

// ClaimJob calls GET /queues/{queue}/next with Bearer auth.
// Returns (nil, nil) on 204 (no jobs available).
func (c *apiClient) ClaimJob(ctx context.Context, queue, token string) (*ClaimResp, error) {
	req, _ := http.NewRequest(http.MethodGet, c.baseURL+"/queues/"+queue+"/next", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	var out ClaimResp
	status, err := c.doJSON(ctx, req, &out)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNoContent {
		return nil, nil
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("claim job: unexpected status %d", status)
	}
	return &out, nil
}

// AckJob calls POST /jobs/{id}/ack with Bearer auth.
func (c *apiClient) AckJob(ctx context.Context, jobID, token string) (int, error) {
	req, _ := http.NewRequest(http.MethodPost, c.baseURL+"/jobs/"+jobID+"/ack", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.hc.Do(req.WithContext(ctx))
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}

// NackJob calls POST /jobs/{id}/nack with Bearer auth.
func (c *apiClient) NackJob(ctx context.Context, jobID, token string) (int, error) {
	req, _ := http.NewRequest(http.MethodPost, c.baseURL+"/jobs/"+jobID+"/nack", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.hc.Do(req.WithContext(ctx))
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}
