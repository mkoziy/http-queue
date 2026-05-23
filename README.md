# http-queue

A durable HTTP queue engine backed by [BadgerDB](https://github.com/dgraph-io/badger) — no external dependencies beyond BadgerDB itself. Workers poll for jobs using claim-based pull (first worker to claim wins), which spreads load naturally without a cursor.

## Architecture

```
┌──────────┐   POST /queues/{queue}/jobs   ┌────────────┐
│  Admin   │   POST /workers               │            │
│  Client  │   DELETE /workers/{id}        │  http-queue │
└──────────┘   ────────────────►            │  (Go)      │
                                            │            │
┌──────────┐   GET  /queues/{queue}/next    │  BadgerDB  │
│  Worker  │   POST /jobs/{id}/ack          │  (durable) │
│  (poll)  │   POST /jobs/{id}/nack         │            │
└──────────┘   ◄────────────────            └────────────┘
```

### Key properties

- **Claim-based pull** — no cursor contention; first worker to claim gets the job.
- **Visibility timeout** — reserved jobs auto-re-queue if not acked in time.
- **Dead-letter queue** — jobs exceeding `MAX_ATTEMPTS` are moved to dead-letter.
- **Worker expiry** — workers that stop polling are automatically deregistered; their reserved jobs get re-queued.
- **Debounced `LastSeen`** — in-memory cache reduces BadgerDB writes on the poll hot path.
- **Reconciliation sweeper** — periodic consistency check repairs orphaned records from crashes.
- **BadgerDB value log GC** — background goroutine prevents unbounded value log growth.

## API

### Admin endpoints (HTTP Basic Auth)

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/queues/{queue}/jobs` | Schedule a new job |
| `POST` | `/workers` | Register a new worker (returns bearer token) |
| `DELETE` | `/workers/{id}` | Deregister a worker (re-queues its reserved jobs) |

### Worker endpoints (Bearer token)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/queues/{queue}/next` | Claim the next pending job (200 + job or 204 No Content) |
| `POST` | `/jobs/{id}/ack` | Acknowledge a job as completed |
| `POST` | `/jobs/{id}/nack` | Negative-acknowledge (re-queue or move to dead-letter) |

## Configuration

| Environment variable | Default | Description |
|---------------------|---------|-------------|
| `PORT` | `8080` | HTTP server listen port |
| `ADMIN_USER` | _(required)_ | Admin Basic Auth username |
| `ADMIN_PASS` | _(required)_ | Admin Basic Auth password |
| `BADGER_PATH` | `/tmp/http-queue` | BadgerDB data directory |
| `VISIBILITY_TIMEOUT` | `30s` | How long a claimed job is reserved before auto-re-queue |
| `WORKER_EXPIRY` | `5m` | Worker considered dead after this long without polling |
| `SWEEP_INTERVAL` | `30s` | How often the sweeper runs expiry/reconciliation |
| `MAX_ATTEMPTS` | `3` | Max claim attempts before job moves to dead-letter |
| `LAST_SEEN_DEBOUNCE` | `30s` | Minimum interval between BadgerDB `LastSeen` writes per worker |

## Getting started

### Prerequisites

- Go 1.26+

### Build and run

```bash
# Build (runs lint first)
make build

# Run tests (race detector enabled)
make test

# Run with required admin credentials
ADMIN_USER=admin ADMIN_PASS=secret go run .

# Or via Makefile
ADMIN_USER=admin ADMIN_PASS=secret make run
```

### Quick demo (using curl)

#### 1. Start the server

```bash
ADMIN_USER=admin ADMIN_PASS=secret \
BADGER_PATH=/tmp/http-queue-demo \
go run .
```

#### 2. Register a worker

```bash
curl -u admin:secret -X POST http://localhost:8080/workers
```

Response (201 Created):

```json
{
  "worker_id": "01JXXXXXXX...",
  "token": "abc123..._base64url_token..."
}
```

> ⚠️ The bearer token is returned **only once** at registration. Store it in your worker process configuration.

#### 3. Schedule a job

```bash
curl -u admin:secret -X POST http://localhost:8080/queues/orders/jobs \
  -H "Content-Type: application/json" \
  -d '{"payload": {"orderId": 42, "action": "process"}}'
```

Response (201 Created):

```json
{
  "id": "01JXXXXXXX...",
  "queue": "orders",
  "status": "pending",
  "created": "2026-05-23T00:00:00Z"
}
```

#### 4. Claim the job (worker side)

```bash
curl -H "Authorization: Bearer YOUR_WORKER_TOKEN" \
  http://localhost:8080/queues/orders/next
```

Response (200 OK) — or 204 No Content if the queue is empty:

```json
{
  "id": "01JXXXXXXX...",
  "queue": "orders",
  "payload": {"orderId": 42, "action": "process"},
  "attempts": 1
}
```

#### 5. Acknowledge the job

```bash
curl -H "Authorization: Bearer YOUR_WORKER_TOKEN" \
  -X POST http://localhost:8080/jobs/01JXXXXXXX.../ack
```

Response: 204 No Content.

#### 6. Nack a job (re-queue or dead-letter)

```bash
curl -H "Authorization: Bearer YOUR_WORKER_TOKEN" \
  -X POST http://localhost:8080/jobs/01JXXXXXXX.../nack
```

Response: 204 No Content.

If the job's `attempts` count has reached `MAX_ATTEMPTS`, it is moved to the dead-letter queue instead of being re-queued.

## Storage layout

http-queue stores all state in BadgerDB using the following key scheme:

```
job:{ulid}                        → JSON: {queue, payload, status, workerID, createdAt, attempts}
queue:{queue}:pending:{ulid}      → "" (index only)
queue:{queue}:reserved:{ulid}     → expiry-unix-timestamp (int64 string)
queue:{queue}:dead:{ulid}         → "" (index only, jobs exceeding MAX_ATTEMPTS)
worker:{worker-id}                → JSON: {tokenHash, lastSeen, registeredAt}
workertoken:{sha256-hex}          → worker-id (reverse index for O(1) bearer token lookup)
```

### Invariants

- Every `queue:*:pending:{ulid}` key has a corresponding `job:{ulid}` record with `status=pending`
- Every `queue:*:reserved:{ulid}` key has a corresponding `job:{ulid}` record with `status=reserved`
- The sweeper reconciliation pass cross-checks these to repair crash-orphaned records
- Queue names must not contain `:` — enforced at job scheduling time

## Security

- **Admin credentials** are loaded from `ADMIN_USER` and `ADMIN_PASS` environment variables and stored only in process memory. They are never written to BadgerDB.
- **Worker bearer tokens** are randomly generated (32 bytes via `crypto/rand`), returned as plaintext **once** at registration, and stored as SHA-256 hex hashes.
- Token comparison uses `crypto/subtle.ConstantTimeCompare` to prevent timing attacks.
- Auth failures return a generic `401 Unauthorized` without revealing which credential component was invalid.

## Project structure

```
.
├── main.go              # Entry point: config, DB, router, sweeper, graceful shutdown
├── config/
│   └── config.go        # Environment-based configuration
├── db/
│   └── db.go            # BadgerDB open/close and key builders
├── token/
│   └── token.go         # Bearer token generation, hashing, verification
├── queue/
│   ├── job.go           # Schedule, claim, ack, nack operations
│   ├── worker.go        # Register, deregister, lookup, touch operations
│   └── sweep.go         # Expiry and reconciliation sweeper
├── api/
│   ├── router.go        # Route wiring with correct middleware
│   ├── middleware.go     # BasicAuth, BearerAuth, logging
│   ├── jobs.go          # POST /queues/{queue}/jobs handler
│   └── workers.go       # Worker admin + claim/ack/nack handlers
├── Makefile             # lint, build, test, run targets
└── .golangci.yml        # Linter configuration
```

## Development

### Available targets

```bash
make lint    # Run golangci-lint
make build   # Build (runs lint first)
make test    # Run tests with race detector
make run     # Quick start (set env vars first)
```

### Testing

```bash
# Run all tests (race-enabled)
make test

# Run a specific test suite
go test -race ./queue/ -run TestScheduleAndClaim
go test -race ./api/ -run TestAdminEndpoints
```

## License

MIT
