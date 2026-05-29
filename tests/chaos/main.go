package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"sync/atomic"
	"time"
)

// counters holds atomic run-wide event tallies.
type counters struct {
	publishes      atomic.Int64
	claims         atomic.Int64
	acks           atomic.Int64
	nacks          atomic.Int64
	abandoned      atomic.Int64
	slowACKs       atomic.Int64
	doubleACKs     atomic.Int64
	restarts       atomic.Int64
	httpErrors     atomic.Int64
	invariantFails atomic.Int64
}

func (c *counters) snapshot() map[string]int64 {
	return map[string]int64{
		"publishes":       c.publishes.Load(),
		"claims":          c.claims.Load(),
		"acks":            c.acks.Load(),
		"nacks":           c.nacks.Load(),
		"abandoned":       c.abandoned.Load(),
		"slow_acks":       c.slowACKs.Load(),
		"double_acks":     c.doubleACKs.Load(),
		"restarts":        c.restarts.Load(),
		"http_errors":     c.httpErrors.Load(),
		"invariant_fails": c.invariantFails.Load(),
	}
}

// cfg holds all parsed CLI flags.
type cfg struct {
	duration          time.Duration
	publishers        int
	workers           int
	seed              int64
	queues            int
	visibilityTimeout time.Duration
	workerExpiry      time.Duration
	sweepInterval     time.Duration
	maxAttempts       int
	restartProb       float64
	keepArtifacts     bool
}

func parseFlags() cfg {
	var c cfg
	flag.DurationVar(&c.duration, "duration", 30*time.Second, "how long to run the chaos test")
	flag.IntVar(&c.publishers, "publishers", 2, "number of publisher goroutines")
	flag.IntVar(&c.workers, "workers", 4, "number of worker goroutines")
	flag.Int64Var(&c.seed, "seed", time.Now().UnixNano(), "RNG seed for reproducibility")
	flag.IntVar(&c.queues, "queues", 3, "number of distinct queue names")
	flag.DurationVar(&c.visibilityTimeout, "visibility-timeout", 3*time.Second, "job visibility timeout passed to server")
	flag.DurationVar(&c.workerExpiry, "worker-expiry", 5*time.Second, "worker expiry duration passed to server")
	flag.DurationVar(&c.sweepInterval, "sweep-interval", 1*time.Second, "server sweep interval passed to server")
	flag.IntVar(&c.maxAttempts, "max-attempts", 3, "max job attempts passed to server")
	flag.Float64Var(&c.restartProb, "restart-probability", 0.0, "probability [0,1] of a server restart event per controller tick")
	flag.BoolVar(&c.keepArtifacts, "keep-artifacts", false, "keep temp dir and server binary after the run")
	flag.Parse()
	return c
}

// newRNG returns a per-caller RNG seeded from the global seed plus a unique salt.
// Each goroutine calls this once so goroutines don't share state.
func newRNG(seed int64, salt uint64) *rand.Rand {
	s := rand.NewPCG(uint64(seed), salt)
	return rand.New(s)
}

// runID generates a short random identifier for this run.
func genRunID(rng *rand.Rand) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 8)
	for i := range b {
		b[i] = charset[rng.IntN(len(charset))]
	}
	return string(b)
}

// genAdminCreds returns a random username and password suitable for Basic Auth.
func genAdminCreds(rng *rand.Rand) (user, pass string) {
	user = fmt.Sprintf("chaos-%s", genRunID(rng))
	pass = fmt.Sprintf("pw-%s", genRunID(rng))
	return
}

// runLogger returns a base logger with run_id and seed attached.
func runLogger(runID string, seed int64) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})).With("run_id", runID, "seed", seed)
}

// actorLogger returns a child logger with an actor field added.
func actorLogger(base *slog.Logger, actor string) *slog.Logger {
	return base.With("actor", actor)
}

func main() {
	c := parseFlags()

	rootRNG := newRNG(c.seed, 0)
	runID := genRunID(rootRNG)
	adminUser, adminPass := genAdminCreds(rootRNG)

	tmpDir, err := os.MkdirTemp("", fmt.Sprintf("chaos-%s-", runID))
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp dir: %v\n", err)
		os.Exit(1)
	}
	badgerPath := fmt.Sprintf("%s/badger", tmpDir)

	log := runLogger(runID, c.seed)
	log.Info("chaos run starting",
		"duration", c.duration.String(),
		"publishers", c.publishers,
		"workers", c.workers,
		"queues", c.queues,
		"admin_user", adminUser,
		"badger_path", badgerPath,
		"tmp_dir", tmpDir,
		"restart_probability", c.restartProb,
		"keep_artifacts", c.keepArtifacts,
	)

	_ = adminPass // used in later tasks
	_ = actorLogger

	var stats counters

	ctx, cancel := context.WithTimeout(context.Background(), c.duration)
	defer cancel()

	// Placeholder: later tasks will wire publishers, workers, controller, and auditor here.
	<-ctx.Done()

	log.Info("chaos run complete", "summary", stats.snapshot())

	if !c.keepArtifacts {
		if err := os.RemoveAll(tmpDir); err != nil {
			log.Warn("failed to remove temp dir", "path", tmpDir, "err", err)
		}
	}

	if fails := stats.invariantFails.Load(); fails > 0 {
		log.Error("invariant failures detected", "count", fails)
		os.Exit(1)
	}
}
