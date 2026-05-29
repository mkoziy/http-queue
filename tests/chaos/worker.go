package main

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// workerState holds the live registration of one chaos worker goroutine.
type workerState struct {
	workerID string
	token    string
	cancel   context.CancelFunc
}

// workerPool manages N concurrent worker goroutines.
type workerPool struct {
	n      int
	queues []string
	ac     *apiClient
	log    *slog.Logger
	stats  *counters
	ledger *ledger
	seed   int64
	visTmt time.Duration // visibility timeout, used to pace slow-ACK

	mu      sync.Mutex
	workers []*workerState // live registrations, for Task 7 kill events
}

// run registers all workers and starts their goroutines, adding them to wg.
func (p *workerPool) run(ctx context.Context, wg *sync.WaitGroup) {
	for i := 0; i < p.n; i++ {
		// salt range 200–299 reserved for workers
		salt := uint64(200 + i)
		rng := newRNG(p.seed, salt)

		resp, err := p.ac.RegisterWorker(ctx)
		if err != nil {
			p.log.Warn("worker registration failed, skipping goroutine", "err", err)
			continue
		}
		p.log.Info("worker registered", "worker_id", resp.WorkerID)

		wctx, cancel := context.WithCancel(ctx)
		ws := &workerState{workerID: resp.WorkerID, token: resp.Token, cancel: cancel}

		p.mu.Lock()
		p.workers = append(p.workers, ws)
		p.mu.Unlock()

		wg.Add(1)
		go p.loop(wctx, wg, ws, rng)
	}
}

// KillWorker cancels a worker goroutine by index and returns its stale token.
// Returns ("", false) if the index is out of range or already dead.
func (p *workerPool) KillWorker(idx int) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if idx < 0 || idx >= len(p.workers) {
		return "", false
	}
	ws := p.workers[idx]
	ws.cancel()
	return ws.token, true
}

// LiveCount returns the number of registered worker slots.
func (p *workerPool) LiveCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.workers)
}

// weighted action constants
const (
	actionACK     = "ack"
	actionNACK    = "nack"
	actionAbandon = "abandon"
	actionSlowACK = "slow_ack"
	actionDouble  = "double_ack"
)

func pickAction(r interface{ IntN(int) int }) string {
	// Weights: ACK=50, NACK=20, abandon=15, slowACK=10, doubleACK=5
	switch n := r.IntN(100); {
	case n < 50:
		return actionACK
	case n < 70:
		return actionNACK
	case n < 85:
		return actionAbandon
	case n < 95:
		return actionSlowACK
	default:
		return actionDouble
	}
}

func (p *workerPool) loop(ctx context.Context, wg *sync.WaitGroup, ws *workerState, rng interface{ IntN(int) int }) {
	defer wg.Done()
	log := p.log.With("worker_id", ws.workerID)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		queue := p.queues[rng.IntN(len(p.queues))]

		claim, err := p.ac.ClaimJob(ctx, queue, ws.token)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			// Transient errors during restarts are already counted in logRT.
			jitter := time.Duration(50+rng.IntN(151)) * time.Millisecond
			select {
			case <-ctx.Done():
				return
			case <-time.After(jitter):
			}
			continue
		}
		if claim == nil {
			// 204 No Content — no job available; back off briefly.
			jitter := time.Duration(50+rng.IntN(151)) * time.Millisecond
			select {
			case <-ctx.Done():
				return
			case <-time.After(jitter):
			}
			continue
		}

		// Record claim before any processing delay.
		p.stats.claims.Add(1)
		p.ledger.recordClaim(claim.ID, claim.Queue, ws.workerID, claim.Attempts)

		action := pickAction(rng)
		log.Info("worker action",
			"job_id", claim.ID,
			"queue", claim.Queue,
			"attempts", claim.Attempts,
			"action", action,
		)

		p.executeAction(ctx, ws, claim, action, log)
	}
}

func (p *workerPool) executeAction(ctx context.Context, ws *workerState, claim *ClaimResp, action string, log *slog.Logger) {
	jobID := claim.ID

	switch action {
	case actionACK:
		status, err := p.ac.AckJob(ctx, jobID, ws.token)
		if err == nil && status == 204 {
			p.stats.acks.Add(1)
			p.ledger.recordACK(jobID, ws.workerID)
		}
		log.Info("ack result", "job_id", jobID, "status", status, "err", errStr(err), "expected", "204")

	case actionNACK:
		status, err := p.ac.NackJob(ctx, jobID, ws.token)
		if err == nil && status == 204 {
			p.stats.nacks.Add(1)
		}
		log.Info("nack result", "job_id", jobID, "status", status, "err", errStr(err), "expected", "204")

	case actionAbandon:
		// Deliberately do nothing — visibility timeout will return job to pending.
		p.stats.abandoned.Add(1)
		log.Info("abandon", "job_id", jobID, "expected", "job returns to pending after visibility timeout")

	case actionSlowACK:
		// Sleep past visibility timeout so the job re-enters pending before our ACK arrives.
		delay := p.visTmt + 200*time.Millisecond
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		status, err := p.ac.AckJob(ctx, jobID, ws.token)
		p.stats.slowACKs.Add(1)
		// If the sweep hasn't fired yet the ACK may still succeed; record it either way.
		if err == nil && status == 204 {
			p.stats.acks.Add(1)
			p.ledger.recordACK(jobID, ws.workerID)
		}
		log.Info("slow_ack result", "job_id", jobID, "status", status, "err", errStr(err),
			"expected", "204 (if sweep hasn't fired) or 404/409", "delay_ms", delay.Milliseconds())

	case actionDouble:
		status1, err1 := p.ac.AckJob(ctx, jobID, ws.token)
		if err1 == nil && status1 == 204 {
			p.stats.acks.Add(1)
			p.ledger.recordACK(jobID, ws.workerID)
		}
		status2, err2 := p.ac.AckJob(ctx, jobID, ws.token)
		p.stats.doubleACKs.Add(1)
		log.Info("double_ack result", "job_id", jobID,
			"status1", status1, "err1", errStr(err1),
			"status2", status2, "err2", errStr(err2),
			"expected", "first 204, second 404 or 409")
	}
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
