# Plan: HTTP Queue Engine Implementation

## Overview
Implement the Go HTTP queue engine described in `docs/plans/20260521-http-queue.md` using BadgerDB for durable storage, `net/http` for the API, and claim-based worker polling. The implementation will keep queue/job/worker state transactional, use environment-backed admin Basic Auth, issue hashed worker bearer tokens, run expiry/reconciliation sweepers, and document usage in a new README.

## Context
- `go.mod` declares module `github.com/mkoziy/http-queue` with Go `1.26`.
- Existing tooling includes `.golangci.yml` and a `Makefile` with `lint`, `build`, `test`, and `run`.
- Current repository has no Go source packages yet.
- `make test` runs `go test -race ./...`; `make build` depends on `make lint`.
- `docs/plans/20260521-http-queue.md` defines the original queue architecture, storage layout, API surface, and task outline.

## Success Criteria
- [ ] Admin users can register/deregister workers and schedule jobs through HTTP endpoints using Basic Auth credentials from environment/config only
- [ ] Workers can claim, ack, and nack jobs using bearer-token authentication
- [ ] Expired reservations and expired workers are reconciled by the sweeper
- [ ] Jobs exceeding max attempts move to a dead-letter index
- [ ] `make test` passes
- [ ] `make lint` passes
- [ ] `make build` passes

## Design Decisions
- Use BadgerDB only for queue state, jobs, worker records, worker token hashes, and indexes.
- Store admin Basic Auth credentials in process configuration loaded from environment variables `ADMIN_USER` and `ADMIN_PASS`; do not persist admin credentials in BadgerDB.
- Keep worker credentials separate from admin credentials: worker bearer tokens are generated at registration, returned once as plaintext, and stored only as SHA-256 hashes in BadgerDB.
- Use stdlib `net/http` routing and middleware instead of adding a router framework.
- Use ULID job IDs for sortable, lexicographic queue indexes.

## Auth / Security
- Admin Basic Auth:
  - Source: `ADMIN_USER` and `ADMIN_PASS` environment variables loaded into `config.Config`.
  - Runtime storage: in-memory `config.Config` fields only.
  - Persistent storage: none; admin credentials must not be written to BadgerDB, logs, README examples as real secrets, or test fixtures outside synthetic test values.
- Worker Bearer Auth:
  - Plain token is returned only when a worker is registered.
  - Token hash is stored in `worker:{worker-id}` and indexed by `workertoken:{sha256-hex}`.
  - Token comparison must use constant-time comparison.
- HTTP auth failures must return `401 Unauthorized` without leaking which credential component failed.

## Key Layout
```text
job:{queue}:{ulid}                → JSON: {payload, status, workerID, createdAt, attempts}
queue:{queue}:pending:{ulid}      → "" (index only)
queue:{queue}:reserved:{ulid}     → expiry-unix-timestamp (int64 string)
queue:{queue}:dead:{ulid}         → "" (index only)
worker:{worker-id}                → JSON: {tokenHash, lastSeen, registeredAt}
workertoken:{sha256-hex}          → worker-id
```

## Invariants
- [ ] Queue names must not contain `:`
- [ ] Every pending index key must correspond to a `status=pending` job record
- [ ] Every reserved index key must correspond to a `status=reserved` job record
- [ ] Claim, ack, nack, registration, and deregistration state changes must be transactional
- [ ] Worker bearer tokens must only be stored as hashes
- [ ] Admin Basic Auth credentials must only come from `config.Config` loaded from environment variables and must never be persisted

## API Surface
```text
# Admin routes, protected by Basic Auth from ADMIN_USER / ADMIN_PASS
POST   /queues/{queue}/jobs
POST   /workers
DELETE /workers/{id}

# Worker routes, protected by Bearer token
GET    /queues/{queue}/next
POST   /jobs/{id}/ack
POST   /jobs/{id}/nack
```

### Task 1: Add Dependencies and Package Skeleton
**Files:**
- Modify: `go.mod`
- Create: `go.sum`
- Create: `config/config.go`
- Create: `db/db.go`
- Create: `token/token.go`
- Create: `queue/job.go`
- Create: `queue/worker.go`
- Create: `queue/sweep.go`
- Create: `api/middleware.go`
- Create: `api/router.go`
- Create: `api/jobs.go`
- Create: `api/workers.go`
- Create: `main.go`

- [ ] Run `go get github.com/dgraph-io/badger/v4`
- [ ] Run `go get github.com/oklog/ulid/v2`
- [ ] Create package directories and placeholder files
- [ ] Run `go mod tidy`
- [ ] Run `make lint` and require a green linter before continuing

### Task 2: Implement Configuration Loading
**Files:**
- Modify: `config/config.go`
- Create: `config/config_test.go`

- [ ] Add `Config` struct with `Port`, `AdminUser`, `AdminPass`, `BadgerPath`, `VisibilityTimeout`, `WorkerExpiry`, `SweepInterval`, `MaxAttempts`, and `LastSeenDebounce`
- [ ] Implement `Load() (*Config, error)` using environment variables with safe defaults, including `ADMIN_USER` and `ADMIN_PASS`
- [ ] Validate that `ADMIN_USER` and `ADMIN_PASS` are the only admin credential source and are stored only in the returned in-memory `Config`
- [ ] Add tests for defaults, overrides, invalid durations, invalid integer values, and admin credential env loading
- [ ] Run `go test -race ./config`
- [ ] Run `make lint` and require a green linter before continuing

### Task 3: Implement BadgerDB Helpers
**Files:**
- Modify: `db/db.go`
- Create: `db/db_test.go`

- [ ] Add `Open(path string) (*badger.DB, error)`
- [ ] Add key builders for jobs, pending indexes, reserved indexes, dead-letter indexes, workers, and worker-token reverse indexes
- [ ] Add helpers for queue-name validation and job ID parsing where needed
- [ ] Add tests for key formats and queue validation
- [ ] Run `go test -race ./db`
- [ ] Run `make lint` and require a green linter before continuing

### Task 4: Implement Token Generation and Hashing
**Files:**
- Modify: `token/token.go`
- Create: `token/token_test.go`

- [ ] Implement `Generate() (plain string, hashed string, err error)` using `crypto/rand`
- [ ] Implement `Hash(plain string) string` using SHA-256 hex encoding
- [ ] Implement `Verify(plain, hashed string) bool` using constant-time comparison
- [ ] Add tests for token uniqueness, hash verification, and failed verification
- [ ] Run `go test -race ./token`
- [ ] Run `make lint` and require a green linter before continuing

### Task 5: Implement Worker Store
**Files:**
- Modify: `queue/worker.go`
- Create: `queue/worker_test.go`

- [ ] Add `Worker` struct with `ID`, `TokenHash`, `LastSeen`, and `RegisteredAt`
- [ ] Implement worker registration with both `worker:{id}` and `workertoken:{hash}` writes in one transaction
- [ ] Implement worker deregistration deleting both worker and token-index keys in one transaction
- [ ] Implement `WorkerByToken` using the reverse index without scanning workers
- [ ] Implement debounced `TouchWorker` using in-memory last-seen tracking plus BadgerDB persistence
- [ ] Add tests for register, lookup by token, deregister cleanup, invalid token, and last-seen debounce
- [ ] Run `go test -race ./queue -run Worker`
- [ ] Run `make lint` and require a green linter before continuing

### Task 6: Implement Job Store
**Files:**
- Modify: `queue/job.go`
- Create: `queue/job_test.go`

- [ ] Add `Job` struct with `ID`, `Queue`, `Payload`, `Status`, `WorkerID`, `CreatedAt`, and `Attempts`
- [ ] Implement `ScheduleJob` to validate queue names and write job plus pending index atomically
- [ ] Implement `ClaimNextJob` using pending prefix seek, atomic pending-to-reserved transition, attempt increment, and bounded conflict retry
- [ ] Implement `AckJob` with worker ownership validation and atomic reserved-index plus job deletion
- [ ] Implement `NackJob` with worker ownership validation, retry handling, and dead-letter movement at max attempts
- [ ] Add tests for schedule, claim, ack, nack, double-claim race, max-attempt dead-lettering, and invalid queue names
- [ ] Run `go test -race ./queue -run Job`
- [ ] Run `make lint` and require a green linter before continuing

### Task 7: Implement Reservation and Worker Sweeper
**Files:**
- Modify: `queue/sweep.go`
- Create: `queue/sweep_test.go`

- [ ] Add `Sweeper` type with BadgerDB, config, and logger dependencies
- [ ] Implement `Start(ctx context.Context)` with ticker lifecycle and context cancellation
- [ ] Implement expired worker deletion including token reverse-index cleanup
- [ ] Implement expired reservation requeue/dead-letter transitions in single transactions
- [ ] Implement reconciliation for reserved-job orphans and phantom pending index keys
- [ ] Add tests for expired reservations, expired workers, orphan reserved jobs, phantom pending indexes, and sweep/claim races
- [ ] Run `go test -race ./queue -run Sweep`
- [ ] Run `make lint` and require a green linter before continuing

### Task 8: Implement HTTP Middleware
**Files:**
- Modify: `api/middleware.go`
- Create: `api/middleware_test.go`

- [ ] Implement Basic Auth middleware that accepts expected username/password from `config.Config.AdminUser` and `config.Config.AdminPass`
- [ ] Ensure Basic Auth middleware does not read or write admin credentials from BadgerDB
- [ ] Implement Bearer Auth middleware for worker routes
- [ ] Add context helpers for storing and retrieving authenticated workers
- [ ] Ensure Bearer Auth calls `WorkerByToken` and `TouchWorker`
- [ ] Add tests for successful auth, missing auth, invalid auth, worker context injection, and proof that admin auth uses config values only
- [ ] Run `go test -race ./api -run Middleware`
- [ ] Run `make lint` and require a green linter before continuing

### Task 9: Implement Admin Handlers
**Files:**
- Modify: `api/jobs.go`
- Modify: `api/workers.go`
- Create: `api/admin_handlers_test.go`

- [ ] Implement `POST /queues/{queue}/jobs` to decode `{"payload": ...}` and return `201` with job ID
- [ ] Implement `POST /workers` to register a worker and return `201` with worker ID and plain token
- [ ] Implement `DELETE /workers/{id}` to deregister a worker and return `204`
- [ ] Add integration tests using real BadgerDB in `t.TempDir()` and synthetic Basic Auth env/config values
- [ ] Run `go test -race ./api -run Admin`
- [ ] Run `make lint` and require a green linter before continuing

### Task 10: Implement Worker Handlers
**Files:**
- Modify: `api/workers.go`
- Create: `api/worker_handlers_test.go`

- [ ] Implement `GET /queues/{queue}/next` returning `200` with a claimed job or `204` when empty
- [ ] Implement `POST /jobs/{id}/ack` returning `204` on successful acknowledgement
- [ ] Implement `POST /jobs/{id}/nack` returning `204` on successful retry/dead-letter transition
- [ ] Map invalid ownership, missing jobs, and malformed paths to appropriate HTTP errors
- [ ] Add integration tests for claim, empty queue, ack, nack, unauthorized requests, and wrong-worker ownership
- [ ] Run `go test -race ./api -run Worker`
- [ ] Run `make lint` and require a green linter before continuing

### Task 11: Implement Router
**Files:**
- Modify: `api/router.go`
- Create: `api/router_test.go`

- [ ] Implement `New(db *badger.DB, cfg *config.Config) http.Handler`
- [ ] Wire admin routes through Basic Auth middleware using `cfg.AdminUser` and `cfg.AdminPass`
- [ ] Wire worker routes through Bearer Auth middleware
- [ ] Add stdlib path parsing helpers for queue names, worker IDs, and job IDs
- [ ] Add route tests for method mismatches, unknown routes, and middleware assignment
- [ ] Run `go test -race ./api -run Router`
- [ ] Run `make lint` and require a green linter before continuing

### Task 12: Implement Main Server Lifecycle
**Files:**
- Modify: `main.go`

- [ ] Load configuration from environment, including `ADMIN_USER` and `ADMIN_PASS`
- [ ] Open BadgerDB for queue/worker state only, not admin credential storage
- [ ] Build router and start HTTP server
- [ ] Start sweeper goroutine with cancellation context
- [ ] Start hourly BadgerDB value-log GC loop using `RunValueLogGC(0.5)`
- [ ] Handle SIGINT/SIGTERM with graceful HTTP shutdown and DB close
- [ ] Run `go test -race ./...`
- [ ] Run `make lint` and require a green linter before continuing

### Task 13: Add Project README
**Files:**
- Create: `README.md`

- [ ] Document project purpose and architecture
- [ ] Add environment variable table with defaults, explicitly listing `ADMIN_USER` and `ADMIN_PASS` as the source of admin Basic Auth credentials
- [ ] State that admin credentials are not stored in BadgerDB and must be supplied by the runtime environment
- [ ] Add curl examples for scheduling jobs, registering workers, claiming, acking, and nacking using placeholder admin credentials
- [ ] Document `make lint`, `make test`, `make build`, and `make run`
- [ ] Include notes about worker tokens, visibility timeout, retries, and dead-letter behavior
- [ ] Run `make lint` and require a green linter before continuing

### Task 14: Final Verification
**Files:**
- Modify: `docs/plans/20260521-http-queue.md`

- [ ] Update the implementation plan checkboxes to reflect completed work
- [ ] Ensure docs clearly distinguish admin Basic Auth env credentials from persisted worker bearer token hashes
- [ ] Run `go mod tidy`
- [ ] Run `make test`
- [ ] Run `make lint` and require a green linter before continuing
- [ ] Run `make build`
- [ ] Manually smoke-test the HTTP API with the README curl flow using a temporary BadgerDB directory and temporary `ADMIN_USER` / `ADMIN_PASS` values
