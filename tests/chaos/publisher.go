package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// publisherPool manages N concurrent publisher goroutines.
type publisherPool struct {
	n      int
	queues []string
	canary string
	ac     *apiClient
	log    *slog.Logger
	stats  *counters
	ledger *ledger
	seed   int64
}

// run launches all publisher goroutines and adds them to wg.
func (p *publisherPool) run(ctx context.Context, wg *sync.WaitGroup) {
	for i := 0; i < p.n; i++ {
		wg.Add(1)
		// salt range 100–199 reserved for publishers
		go p.loop(ctx, wg, uint64(100+i))
	}
}

func (p *publisherPool) loop(ctx context.Context, wg *sync.WaitGroup, salt uint64) {
	defer wg.Done()
	rng := newRNG(p.seed, salt)
	log := p.log.With("publisher_salt", salt)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		queue := p.queues[rng.IntN(len(p.queues))]

		// Use the canary marker rarely (~5%), otherwise a random marker.
		var marker string
		if rng.IntN(20) == 0 {
			marker = p.canary
		} else {
			marker = fmt.Sprintf("pub-%d", rng.IntN(100000))
		}
		payload := map[string]any{
			"marker": marker,
			"ts":     time.Now().UnixMilli(),
		}

		log.Debug("publishing job", "queue", queue, "marker", marker)
		start := time.Now()
		resp, err := p.ac.PublishJob(ctx, queue, payload)
		dur := time.Since(start)

		if err != nil {
			if ctx.Err() != nil {
				return
			}
			// Transient errors during restarts are expected; already counted in logRT.
			log.Debug("publish attempt failed", "queue", queue, "err", err, "duration_ms", dur.Milliseconds())
		} else {
			p.stats.publishes.Add(1)
			p.ledger.recordPublish(resp.ID, queue, marker, time.Now())
			log.Info("published job",
				"job_id", resp.ID,
				"queue", queue,
				"marker", marker,
				"duration_ms", dur.Milliseconds(),
			)
		}

		// Randomized jitter: 10–100 ms between publishes.
		jitter := time.Duration(10+rng.IntN(91)) * time.Millisecond
		select {
		case <-ctx.Done():
			return
		case <-time.After(jitter):
		}
	}
}
