# HTTP Queue Engine — Implementation Plan

## Context

Go HTTP queue engine backed by BadgerDB. Workers are generic (not queue-bound), registered by an upstream server via Admin API. Workers poll for jobs using claim-based pull — first worker to claim gets the job, which naturally spreads load. No external dependencies beyond BadgerDB.

## Design Decisions

- **BadgerDB** for durable storage; all state lives there
- **Claim-based pull** (not round-robin cursor) for natural load spreading
- **net/http stdlib** only — no router framework
- **ULID** job IDs for time-ordered lexicographic scanning
- **Custom indexes** (`queue:{queue}:pending:{ulid}`) so `GET /next` is a prefix seek, not a full scan
- **Visibility timeout** — reserved jobs re-queue automatically if not acked in time
- **Worker last-seen expiry** — worker expires if it stops polling; its reserved jobs re-queue too
- **Background sweep goroutine** handles all expiry, runs every `SWEEP_INTERVAL` seconds
- **Nack retry cap** — job moves to dead-letter index after `MAX_ATTEMPTS` claims; prevents poison-pill loops
- **Worker token reverse index** — `workertoken:{hash}` → worker-id for O(1) bearer token auth lookup
- **Debounced `LastSeen` write** — only flush to BadgerDB if >N seconds since last write (tracked in-memory), reducing write amplification on the poll hot path
- **Queue name validation** — names must not contain `:` (key separator); enforced at schedule time
- **BadgerDB value log GC** — periodic `RunValueLogGC(0.5)` to prevent unbounded value log growth

## Key Layout

```
job:{queue}:{ulid}                → JSON: {payload, status, workerID, createdAt, attempts}
queue:{queue}:pending:{ulid}      → "" (index only)
queue:{queue}:reserved:{ulid}     → expiry-unix-timestamp (int64 string)
queue:{queue}:dead:{ulid}         → "" (index only, jobs exceeding MAX_ATTEMPTS)
worker:{worker-id}                → JSON: {tokenHash, lastSeen, registeredAt}
workertoken:{sha256-hex}          → worker-id (reverse index for O(1) token lookup)
```

### Invariants
- Every `queue:*:reserved:{ulid}` key must have a corresponding `job:*` record with `status=reserved`
- Every `queue:*:pending:{ulid}` key must have a corresponding `job:*` record with `status=pending`
- The sweeper reconciliation pass cross-checks these to catch crash-orphaned records

## Auth

- Admin API: HTTP Basic Auth (`ADMIN_USER` / `ADMIN_PASS`)
- Worker API: Bearer token (issued at registration, stored hashed in BadgerDB)

## API Surface

```
# Admin (Basic Auth)
POST   /queues/{queue}/jobs        schedule a job
POST   /workers                    register worker → {worker_id, token}
DELETE /workers/{id}               deregister worker

# Worker (Bearer token)
GET    /queues/{queue}/next        claim next job (200+job or 204)
POST   /jobs/{id}/ack              mark done
POST   /jobs/{id}/nack             re-queue immediately
```

---

## Tasks

### 1. Bootstrap
- [x] `go mod init github.com/mkoziy/http-queue`
- [ ] `go get github.com/dgraph-io/badger/v4`
- [ ] `go get github.com/oklog/ulid/v2`
- [ ] Create directory structure: `config/`, `db/`, `queue/`, `api/`, `token/`
- [x] Set up `.golangci.yml` with strict curated linter configuration
- [x] Run `make lint` to verify configuration — **lint must pass cleanly before any implementation task below proceeds** (verified: config valid, no Go source files yet)

### 2. Config (`config/config.go`)
- [ ] Struct with all env vars: `PORT`, `ADMIN_USER`, `ADMIN_PASS`, `BadgerPath`, `VisibilityTimeout`, `WorkerExpiry`, `SweepInterval`, `MaxAttempts`, `LastSeenDebounce`
- [ ] `Load() (*Config, error)` reads from env with defaults

### 3. DB layer (`db/db.go`)
- [ ] `Open(path string) (*badger.DB, error)` wrapper
- [ ] Key builder helpers: `JobKey`, `PendingIndexKey`, `ReservedIndexKey`, `WorkerKey`
- [ ] `Close()` with flush

### 4. Token package (`token/token.go`)
- [ ] `Generate() (plain, hashed string, error)` — crypto/rand 32 bytes, base64url plain, sha256 hash stored
- [ ] `Hash(plain string) string`
- [ ] `Verify(plain, hashed string) bool`

### 5. Worker store (`queue/worker.go`)
- [ ] `Worker` struct: `ID`, `TokenHash`, `LastSeen`, `RegisteredAt`
- [ ] `RegisterWorker(db, cfg) (id, token string, err error)` — writes `worker:{id}` + `workertoken:{hash}` in one txn
- [ ] `DeregisterWorker(db, id string) error` — deletes `worker:{id}` + `workertoken:{hash}` in one txn; reserved jobs re-queued by next sweep
- [ ] `TouchWorker(db, id string) error` — debounced: update in-memory last-seen always; flush to BadgerDB only if >(`WorkerExpiry`/2) since last flush (tracked in `sync.Map`)
- [ ] `WorkerByToken(db, plainToken string) (*Worker, error)` — hash token, seek `workertoken:{hash}` for worker-id, then load `worker:{id}`; O(1), no scan

### 6. Job store (`queue/job.go`)
- [ ] `Job` struct: `ID`, `Queue`, `Payload json.RawMessage`, `Status`, `WorkerID`, `CreatedAt`, `Attempts int`
- [ ] `ScheduleJob(db, queue string, payload json.RawMessage) (*Job, error)` — validate queue name (no `:`), write job + pending index in one txn
- [ ] `ClaimNextJob(db, queue, workerID string, visibilityTimeout time.Duration) (*Job, error)` — prefix seek on pending index, increment `Attempts`, atomic claim with conflict retry (up to N retries); all three writes (delete pending index, write reserved index, update job record) in a single txn
- [ ] `AckJob(db, jobID, workerID string) error` — verify worker owns it; delete reserved index + job record in one txn
- [ ] `NackJob(db, jobID, workerID string) error` — verify worker owns it; if `Attempts >= MAX_ATTEMPTS` move to dead-letter (`queue:{queue}:dead:{ulid}`) else move reserved→pending index; all writes in one txn

### 7. Sweep (`queue/sweep.go`)
- [ ] `Sweeper` struct with db, cfg, logger
- [ ] `Start(ctx context.Context)` — ticker loop
- [ ] `sweep()` — single pass:
  1. Scan `worker:*`, collect expired worker IDs (last-seen > `WorkerExpiry`), delete `worker:{id}` + `workertoken:{hash}` in one txn per worker
  2. Scan `queue:*:reserved:*`, re-queue expired reservations and reservations owned by expired workers: **delete reserved index + write pending index + update job status must be a single BadgerDB transaction** to prevent sweep/claim race
  3. Reconciliation: scan all `job:*` records with `status=reserved` and verify a matching `queue:*:reserved:*` key exists; re-queue orphans (crash recovery). Scan `queue:*:pending:*` and verify matching job record exists; delete phantom index keys.
- [ ] Re-queue respects `MAX_ATTEMPTS`: if `job.Attempts >= MAX_ATTEMPTS`, move to dead-letter instead

### 8. Middleware (`api/middleware.go`)
- [ ] `BasicAuth(user, pass string) func(http.Handler) http.Handler`
- [ ] `BearerAuth(db *badger.DB) func(http.Handler) http.Handler` — extracts token, looks up worker, touches last-seen, injects worker into context
- [ ] Context key type + helpers: `WorkerFromCtx(ctx) *queue.Worker`

### 9. Handlers — Admin jobs (`api/jobs.go`)
- [ ] `POST /queues/{queue}/jobs` — decode `{"payload": {...}}`, call `ScheduleJob`, return 201 + job ID

### 10. Handlers — Admin workers (`api/workers.go`)
- [ ] `POST /workers` — call `RegisterWorker`, return 201 + `{worker_id, token}`
- [ ] `DELETE /workers/{id}` — call `DeregisterWorker`, return 204

### 11. Handlers — Worker API (`api/workers.go` continued)
- [ ] `GET /queues/{queue}/next` — call `ClaimNextJob`, return 200+JSON or 204
- [ ] `POST /jobs/{id}/ack` — call `AckJob`, return 204
- [ ] `POST /jobs/{id}/nack` — call `NackJob`, return 204

### 12. Router (`api/router.go`)
- [ ] `New(db, cfg) http.Handler` wires all routes with correct middleware
- [ ] Path param extraction helper (stdlib: parse from URL path)

### 13. Main (`main.go`)
- [ ] Load config, open BadgerDB, build router
- [ ] Start sweeper goroutine with context
- [ ] Start BadgerDB value log GC ticker (every 1h, calls `db.RunValueLogGC(0.5)`) to prevent unbounded value log growth
- [ ] `http.ListenAndServe` with graceful shutdown on SIGINT/SIGTERM

### 14. Tests
- [ ] `queue/job_test.go` — schedule, claim, ack, nack, double-claim race, nack at MAX_ATTEMPTS moves to dead-letter, queue name with `:` rejected
- [ ] `queue/worker_test.go` — register, deregister (verify both `worker:` and `workertoken:` keys removed), token verify, `WorkerByToken` O(1) path
- [ ] `queue/sweep_test.go` — expired reservation re-queue, expired worker job re-queue, reconciliation of orphaned job records, sweep+claim race (goroutines racing sweep and claim on same job)
- [ ] `api/` — integration tests with real BadgerDB (each test gets a fresh `t.TempDir()`; defer `db.Close()`)

### 15. Makefile + README
- [x] `Makefile` with `build`, `test`, `lint`, `run` targets
- [x] `make build` depends on `lint` — **lint must pass before building**
- [x] `make test` is independent of `lint` — fast test iteration without lint gate
- [x] `make run` available for quick manual testing
- [ ] README with env vars table and curl examples
