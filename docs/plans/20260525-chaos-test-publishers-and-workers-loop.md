# Plan: Chaos Test Publishers And Workers Loop

## Overview
Add a standalone Go chaos orchestrator in `tests/chaos/main.go` that builds and runs the real `http-queue` server, then continuously publishes and processes jobs through HTTP with randomized ACK, NACK, no-ACK, slow-ACK, double-ACK, worker-kill, stale-token, and restart scenarios. The test will log every actor and HTTP request as structured JSON and finish with a BadgerDB-backed invariant audit.

## Context
- The server is configured entirely through environment variables in `config/config.go`, including `PORT`, `PORT_FILE`, `ADMIN_USER`, `ADMIN_PASS`, `BADGER_PATH`, `VISIBILITY_TIMEOUT`, `WORKER_EXPIRY`, `SWEEP_INTERVAL`, `MAX_ATTEMPTS`, and `LAST_SEEN_DEBOUNCE`.
- The real API uses admin Basic Auth for `POST /queues/{queue}/jobs`, `POST /workers`, and `DELETE /workers/{id}`.
- Worker operations use Bearer auth for `GET /queues/{queue}/next`, `POST /jobs/{id}/ack`, and `POST /jobs/{id}/nack`.
- There is no health endpoint; existing E2E conventions use unauthenticated `POST /workers` expecting `401` as readiness.
- BadgerDB keys follow the documented layout: `job:{id}`, `queue:{queue}:pending:{id}`, `queue:{queue}:reserved:{id}`, `queue:{queue}:dead:{id}`, `worker:{id}`, and `workertoken:{hash}`.

## Success Criteria
- [ ] `go run ./tests/chaos -duration=10s -publishers=2 -workers=4 -seed=1` builds the server, starts it with isolated BadgerDB state, runs publisher/worker chaos loops, logs structured JSON events, audits invariants, and exits `0`.
- [ ] `go run ./tests/chaos -duration=30s -publishers=4 -workers=8 -seed=123 -restart-probability=0.15` exercises server restarts and completes without leaked server processes or temporary DB directories.
- [ ] `go test -race ./...` passes after adding the chaos package.
- [ ] `make chaos` runs a short deterministic chaos test suitable for local/CI smoke coverage.
- [ ] Failure reports include the seed, run ID, server DB path, actor summaries, HTTP failures, and invariant violations.

## Design Decisions
- Keep the chaos test as an external orchestrator binary so it exercises the real HTTP server and BadgerDB persistence path.
- Use the repository’s actual API shape, including Basic Auth for publishing jobs; do not use the brainstorm’s older `/jobs` unauthenticated assumption.
- Track ACKed jobs in the orchestrator ledger because ACK deletes job records from BadgerDB.
- Stop the server before opening BadgerDB for the final audit to avoid file-lock conflicts.
- Make randomness deterministic from a seed and log the seed on startup for reproducible failures.

## Invariants
- Every published job is either ACKed in the chaos ledger or present exactly once in BadgerDB as pending, reserved, or dead.
- Every queue index points to an existing `job:{id}` record.
- Every pending index points to a job with `status=pending`.
- Every reserved index points to a job with `status=reserved`.
- Every dead index points to a job with `status=dead`.
- No job has multiple queue indexes at once.
- Every live reserved job references an existing worker record.
- Every ACK recorded by the ledger was preceded by a successful claim for the same worker.

### Task 1: Add Chaos Make Target

**Files:**
- Modify: `Makefile`

- [x] Add `chaos` to `.PHONY`.
- [x] Add a `make chaos` target that runs `go run ./tests/chaos -duration=15s -publishers=3 -workers=5 -seed=1`.
- [x] Keep `make test` unchanged so normal unit tests remain fast and deterministic.
- [x] Ensure the new target exits non-zero when the orchestrator reports invariant failures.

### Task 2: Create Orchestrator Configuration And Logging

**Files:**
- Create: `tests/chaos/main.go`

- [x] Define CLI flags for `duration`, `publishers`, `workers`, `seed`, `queues`, `visibility-timeout`, `worker-expiry`, `sweep-interval`, `max-attempts`, `restart-probability`, and `keep-artifacts`.
- [x] Initialize a seeded RNG strategy that is concurrency-safe or provides per-goroutine child RNGs.
- [x] Generate a `run_id`, random admin credentials, and a temporary BadgerDB path.
- [x] Create a shared `slog` JSON logger that always includes `run_id`, `seed`, and actor fields.
- [x] Add summary counters for publishes, claims, ACKs, NACKs, abandoned jobs, slow ACKs, double ACKs, restarts, HTTP errors, and invariant failures.

### Task 3: Implement Server Lifecycle Manager

**Files:**
- Modify: `tests/chaos/main.go`

- [x] Build the server into a temporary binary with `go build -o <temp>/http-queue-chaos-server .`.
- [x] Start the server with `PORT=0`, `PORT_FILE=<temp>/port`, generated admin credentials, temporary `BADGER_PATH`, and fast chaos timing environment variables.
- [x] Wait for readiness by reading `PORT_FILE` and polling `POST /workers` until it returns `401`.
- [x] Implement graceful `SIGTERM` restart using the same BadgerDB path, with fallback kill on timeout.
- [x] Register cleanup that terminates the process and removes temp artifacts unless `-keep-artifacts` is set.

### Task 4: Add Instrumented HTTP Client Helpers

**Files:**
- Modify: `tests/chaos/main.go`

- [x] Create an `http.Client` with timeout and `DisableKeepAlives: true`.
- [x] Wrap transport with a logging `RoundTripper` that records method, URL path, status, duration, request errors, and actor.
- [x] Implement helpers for admin-authenticated worker registration, worker deregistration, job publishing, claiming next jobs, ACK, and NACK.
- [x] Decode the actual response shapes: scheduled jobs return `id`; workers return `worker_id` and `token`; claims return `id`, `queue`, `payload`, and `attempts`.
- [x] Treat transient connection errors during restarts as expected retryable chaos events while still counting and logging them.

### Task 5: Implement Concurrent Publisher Pool

**Files:**
- Modify: `tests/chaos/main.go`

- [x] Start `N` publisher goroutines that loop until context cancellation.
- [x] Randomly select queue names from a small run-scoped set and generate varied JSON payloads, including one canary payload per run.
- [x] Publish through `POST /queues/{queue}/jobs` with admin Basic Auth and randomized jitter/backoff.
- [x] Record every successful published job in a thread-safe ledger by job ID, queue, payload marker, and timestamp.
- [x] Log each publish attempt and successful job ID with latency and queue metadata.

### Task 6: Implement Worker Pool And Edge Actions

**Files:**
- Modify: `tests/chaos/main.go`

- [x] Register each worker through `POST /workers` using admin Basic Auth and store `worker_id` plus bearer token.
- [x] Continuously poll `GET /queues/{queue}/next` with randomized queue selection and jitter.
- [x] On successful claim, record claim ownership in the ledger before simulating processing delay.
- [x] Randomly choose weighted actions: ACK, NACK, no-ACK abandon, slow ACK past visibility timeout, and double ACK.
- [x] Log every worker action with `worker_id`, job ID, queue, attempts, action, result status, and expected edge-case outcome.

### Task 7: Add Chaos Controller Events

**Files:**
- Modify: `tests/chaos/main.go`

- [ ] Run a controller goroutine that wakes at randomized intervals until test cancellation.
- [ ] Implement worker kill events by canceling selected worker goroutines and moving their tokens into a stale-token graveyard.
- [ ] Implement burst publish events that synchronously enqueue a configurable burst of jobs.
- [ ] Implement server `SIGTERM` restart events using the lifecycle manager and same BadgerDB path.
- [ ] Implement stale-token probes that attempt claim/ACK/NACK requests with dead worker tokens and log expected `401` responses.

### Task 8: Implement Ledger And Final Auditor

**Files:**
- Modify: `tests/chaos/main.go`

- [ ] Define ledger structures for published jobs, successful claims, ACKs, NACKs, dead/stale workers, and anomalous HTTP responses.
- [ ] After the run context ends, stop all actors, terminate the server cleanly, and open BadgerDB read-only.
- [ ] Scan BadgerDB keys and reconstruct job records plus pending, reserved, dead, worker, and worker-token indexes.
- [ ] Check the documented invariants and compare DB state against the orchestrator ledger.
- [ ] Print a compact JSON failure report and exit non-zero if any invariant fails.

### Task 9: Add Documentation For Running Chaos Tests

**Files:**
- Modify: `README.md`

- [ ] Add a “Chaos Tests” subsection under Development or Testing.
- [ ] Document `make chaos` and direct `go run ./tests/chaos` usage.
- [ ] Explain seed-based reproduction and include an example command using a failed seed.
- [ ] Document that the chaos test builds and runs the real server with a temporary BadgerDB directory.
- [ ] Mention `-keep-artifacts` for debugging failed runs.
