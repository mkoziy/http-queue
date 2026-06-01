package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// controller fires randomized chaos events at irregular intervals.
type controller struct {
	ac          *apiClient
	srv         *serverMgr
	wrkPool     *workerPool
	queues      []string
	visTmt      time.Duration
	log         *slog.Logger
	stats       *counters
	ledger      *ledger
	seed        int64
	restartProb float64
	events      *eventWriter
}

func (c *controller) run(ctx context.Context, wg *sync.WaitGroup) {
	wg.Add(1)
	go c.loop(ctx, wg)
}

func (c *controller) loop(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	// salt 300 reserved for the controller
	rng := newRNG(c.seed, 300)
	log := c.log

	for {
		// Wake at a random interval between 500ms and 2s.
		interval := time.Duration(500+rng.IntN(1500)) * time.Millisecond
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}

		// Pick an event type based on weights and configured probabilities.
		// Events: worker-kill (30%), burst-publish (40%), stale-token-probe (30%),
		// plus server restart governed by restartProb independently.

		n := rng.IntN(100)
		switch {
		case n < 30:
			c.doWorkerKill(ctx, rng, log)
		case n < 70:
			c.doBurstPublish(ctx, rng, log)
		default:
			c.doStaleTokenProbe(ctx, rng, log)
		}

		// Server restart is checked independently.
		if c.restartProb > 0 && rng.Float64() < c.restartProb {
			c.doServerRestart(ctx, log)
		}
	}
}

func (c *controller) doWorkerKill(ctx context.Context, rng interface{ IntN(int) int }, log *slog.Logger) {
	count := c.wrkPool.LiveCount()
	if count == 0 {
		return
	}
	idx := rng.IntN(count)
	token, ok := c.wrkPool.KillWorker(idx)
	if !ok {
		return
	}
	c.ledger.addStaleToken(token)
	log.Info("controller: worker killed", "idx", idx)
	c.events.Write("info", "controller", "worker_killed", map[string]any{
		"idx": idx,
	})
}

func (c *controller) doBurstPublish(ctx context.Context, rng interface{ IntN(int) int }, log *slog.Logger) {
	burst := 3 + rng.IntN(5) // 3–7 jobs
	log.Info("controller: burst publish", "count", burst)
	c.events.Write("info", "controller", "burst_publish", map[string]any{"count": burst})
	for i := 0; i < burst; i++ {
		if ctx.Err() != nil {
			return
		}
		queue := c.queues[rng.IntN(len(c.queues))]
		ttl := pickTTLVariant(rng, c.visTmt)
		payload := map[string]any{
			"marker":      fmt.Sprintf("burst-%d", rng.IntN(100000)),
			"burst":       true,
			"ttl_variant": ttl.Name,
		}
		if ttl.Seconds != nil {
			payload["ttl_seconds"] = *ttl.Seconds
		}
		resp, err := c.ac.PublishJob(ctx, queue, payload, ttl.Seconds)
		if err != nil {
			log.Debug("burst publish failed", "err", err)
			continue
		}
		c.stats.publishes.Add(1)
		c.ledger.recordPublish(resp.ID, queue, "burst", ttl.Name, ttl.Seconds, resp.CreatedAt)
		log.Debug("burst job published", "job_id", resp.ID, "queue", queue, "ttl_variant", ttl.Name)
		c.events.Write("info", "controller", "job_published", map[string]any{
			"job_id":      resp.ID,
			"queue":       queue,
			"marker":      "burst",
			"ttl_variant": ttl.Name,
			"ttl_seconds": ttl.SecondsValue(),
		})
	}
}

func (c *controller) doServerRestart(ctx context.Context, log *slog.Logger) {
	log.Info("controller: triggering server restart")
	if err := c.srv.restart(); err != nil {
		log.Warn("server restart failed", "err", err)
		return
	}
	// Update the apiClient's baseURL to the new port after restart.
	c.ac.baseURL = c.srv.baseURL
	c.stats.restarts.Add(1)
	log.Info("controller: server restarted", "new_base_url", c.srv.baseURL)
	c.events.Write("info", "controller", "server_restarted", map[string]any{
		"base_url": c.srv.baseURL,
	})
}

func (c *controller) doStaleTokenProbe(ctx context.Context, rng interface{ IntN(int) int }, log *slog.Logger) {
	tokens := c.ledger.pickStaleTokens()
	if len(tokens) == 0 {
		return
	}
	token := tokens[rng.IntN(len(tokens))]
	queue := c.queues[rng.IntN(len(c.queues))]

	// Attempt claim with stale token — expect 401.
	claim, err := c.ac.ClaimJob(ctx, queue, token)
	if err == nil && claim == nil {
		// 204 is also acceptable (no job but auth passed — token reuse not fully expired yet).
		log.Debug("stale token probe: no job available (204)", "expected", "401 or 204")
	} else if err != nil {
		log.Debug("stale token probe: connection error", "err", err)
	} else {
		log.Info("stale token probe: got claim (unexpected)", "job_id", claim.ID, "expected", "401")
	}

	// Also probe ACK and NACK with a made-up job ID using the stale token.
	fakeJobID := "stale-probe-000000000000"
	statusACK, _ := c.ac.AckJob(ctx, fakeJobID, token)
	log.Info("stale token probe ack", "status", statusACK, "expected", "401 or 404")

	statusNACK, _ := c.ac.NackJob(ctx, fakeJobID, token)
	log.Info("stale token probe nack", "status", statusNACK, "expected", "401 or 404")
	c.events.Write("info", "controller", "stale_token_probe", map[string]any{
		"queue":        queue,
		"claim_job_id": claimID(claim),
		"claim_ok":     err == nil && claim != nil,
		"claim_err":    errStr(err),
		"ack_status":   statusACK,
		"nack_status":  statusNACK,
	})
}

func claimID(claim *ClaimResp) string {
	if claim == nil {
		return ""
	}
	return claim.ID
}
