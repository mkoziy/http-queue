package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
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

// genRunID generates a short random identifier for this run.
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

// serverMgr builds, starts, and restarts the real http-queue server process.
type serverMgr struct {
	binaryPath  string
	portFile    string
	badgerPath  string
	adminUser   string
	adminPass   string
	c           cfg
	log         *slog.Logger
	proc        *exec.Cmd
	baseURL     string
}

// build compiles the server binary into the temp directory.
func (s *serverMgr) build() error {
	s.log.Info("building server binary", "output", s.binaryPath)
	cmd := exec.Command("go", "build", "-o", s.binaryPath, ".")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// serverEnv assembles the environment for a server process.
func (s *serverMgr) serverEnv() []string {
	return []string{
		"PORT=0",
		fmt.Sprintf("PORT_FILE=%s", s.portFile),
		fmt.Sprintf("ADMIN_USER=%s", s.adminUser),
		fmt.Sprintf("ADMIN_PASS=%s", s.adminPass),
		fmt.Sprintf("BADGER_PATH=%s", s.badgerPath),
		fmt.Sprintf("VISIBILITY_TIMEOUT=%s", s.c.visibilityTimeout),
		fmt.Sprintf("WORKER_EXPIRY=%s", s.c.workerExpiry),
		fmt.Sprintf("SWEEP_INTERVAL=%s", s.c.sweepInterval),
		fmt.Sprintf("MAX_ATTEMPTS=%d", s.c.maxAttempts),
		// fast debounce so workers expire quickly during chaos
		"LAST_SEEN_DEBOUNCE=500ms",
		// pass through PATH so the binary can find shared libs if needed
		fmt.Sprintf("PATH=%s", os.Getenv("PATH")),
		fmt.Sprintf("HOME=%s", os.Getenv("HOME")),
	}
}

// start launches the server process and waits until it is ready.
func (s *serverMgr) start() error {
	// Remove stale port file from a previous run.
	_ = os.Remove(s.portFile)

	cmd := exec.Command(s.binaryPath)
	cmd.Env = s.serverEnv()
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start server: %w", err)
	}
	s.proc = cmd
	s.log.Info("server process started", "pid", cmd.Process.Pid)

	if err := s.waitReady(); err != nil {
		_ = cmd.Process.Kill()
		return err
	}
	return nil
}

// waitReady blocks until the server writes its port file and returns 401 on POST /workers.
func (s *serverMgr) waitReady() error {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		port, err := s.readPort()
		if err != nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		s.baseURL = fmt.Sprintf("http://127.0.0.1:%d", port)
		if s.probeReady() {
			s.log.Info("server ready", "base_url", s.baseURL)
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("server did not become ready within 15s")
}

func (s *serverMgr) readPort() (int, error) {
	data, err := os.ReadFile(s.portFile)
	if err != nil {
		return 0, err
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("invalid port file content: %q", data)
	}
	if port <= 0 {
		return 0, fmt.Errorf("port not yet written")
	}
	return port, nil
}

func (s *serverMgr) probeReady() bool {
	// An unauthenticated POST /workers returning 401 means the server is up.
	resp, err := http.Post(s.baseURL+"/workers", "application/json", nil) //nolint:noctx
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusUnauthorized
}

// restart sends SIGTERM and waits up to 5 s, then kills, then starts fresh.
func (s *serverMgr) restart() error {
	s.log.Info("restarting server", "pid", s.proc.Process.Pid)
	if err := s.proc.Process.Signal(sigTERM()); err != nil {
		s.log.Warn("sigterm failed, killing", "err", err)
		_ = s.proc.Process.Kill()
	}

	done := make(chan error, 1)
	go func() { done <- s.proc.Wait() }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		s.log.Warn("server did not exit after SIGTERM, killing")
		_ = s.proc.Process.Kill()
		<-done
	}

	return s.start()
}

// stop terminates the server process gracefully.
func (s *serverMgr) stop() {
	if s.proc == nil || s.proc.Process == nil {
		return
	}
	s.log.Info("stopping server", "pid", s.proc.Process.Pid)
	if err := s.proc.Process.Signal(sigTERM()); err != nil {
		_ = s.proc.Process.Kill()
	}
	done := make(chan error, 1)
	go func() { done <- s.proc.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = s.proc.Process.Kill()
		<-done
	}
	s.log.Info("server stopped")
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

	srv := &serverMgr{
		binaryPath: fmt.Sprintf("%s/http-queue-chaos-server", tmpDir),
		portFile:   fmt.Sprintf("%s/port", tmpDir),
		badgerPath: badgerPath,
		adminUser:  adminUser,
		adminPass:  adminPass,
		c:          c,
		log:        actorLogger(log, "lifecycle"),
	}

	if err := srv.build(); err != nil {
		log.Error("server build failed", "err", err)
		os.Exit(1)
	}
	if err := srv.start(); err != nil {
		log.Error("server start failed", "err", err)
		os.Exit(1)
	}

	var stats counters
	led := newLedger()

	ac := &apiClient{
		hc:        newClient(actorLogger(log, "http"), &stats),
		baseURL:   srv.baseURL,
		adminUser: adminUser,
		adminPass: adminPass,
		log:       actorLogger(log, "http"),
		stats:     &stats,
	}

	// Build the run-scoped queue name set and canary marker.
	queueNames := make([]string, c.queues)
	for i := range queueNames {
		queueNames[i] = fmt.Sprintf("chaos-q-%d", i)
	}
	canary := fmt.Sprintf("canary-%s", runID)

	log.Info("queue set", "queues", queueNames, "canary", canary)

	pubPool := &publisherPool{
		n:      c.publishers,
		queues: queueNames,
		canary: canary,
		ac:     ac,
		log:    actorLogger(log, "publisher"),
		stats:  &stats,
		ledger: led,
		seed:   c.seed,
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.duration)
	defer cancel()

	var wg sync.WaitGroup
	pubPool.run(ctx, &wg)

	// Placeholder: Task 6 adds worker pool, Task 7 adds controller.
	<-ctx.Done()
	wg.Wait()

	srv.stop()

	log.Info("ledger summary", "published", led.publishedCount())

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
