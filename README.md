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
├── Dockerfile           # Multi-stage production Docker build
├── Makefile             # lint, build, test, run, docker-build, docker-build-multi, release
├── .dockerignore        # Docker build context exclusions
└── .golangci.yml        # Linter configuration
```

## Docker

A multi-stage `Dockerfile` produces a statically linked binary in a minimal `alpine` runtime image. The service runs as a non-root user.

### Build locally

```bash
make docker-build
```

This runs tests first, then builds a local image tagged as `ghcr.io/mkoziy/http-queue:latest` (overridable via `IMAGE` and `VERSION` variables):

```bash
# Custom image name / version
make docker-build IMAGE=my-registry/http-queue VERSION=1.0.0
```

### Run the container

```bash
docker run --rm \
  -e ADMIN_USER=admin \
  -e ADMIN_PASS=secret \
  -e BADGER_PATH=/data \
  -v http-queue-data:/data \
  -p 8080:8080 \
  ghcr.io/mkoziy/http-queue:latest
```

Mount a volume for `BADGER_PATH` to persist queue state across container restarts.

### Multi-architecture builds

```bash
make docker-build-multi
```

Uses `docker buildx` to build and push multi-arch images for the platforms specified in `PLATFORMS` (default: `linux/amd64,linux/arm64`). This target pushes to a registry, so you must be authenticated first (see [Releasing](#releasing) for login instructions). For local-only builds, use `make docker-build` instead.

## Releasing

Releases are published to GitHub Container Registry (GHCR) using the `release` Makefile target. Before running a release, ensure you are authenticated with GHCR:

```bash
echo $GITHUB_TOKEN | docker login ghcr.io -u mkoziy --password-stdin
```

### Create a release

```bash
make release VERSION=1.0.0
```

This will:

1. Run tests with the race detector
2. Verify the working tree is clean and the tag does not already exist
3. Build and push multi-architecture images (`linux/amd64`, `linux/arm64`) to `ghcr.io/mkoziy/http-queue` as:
   - `ghcr.io/mkoziy/http-queue:1.0.0` (versioned)
   - `ghcr.io/mkoziy/http-queue:latest` (rolling)
4. Create an annotated git tag `1.0.0`
5. Push the tag to `origin`

Use semantic versioning (`X.Y.Z`) for all releases.

## Development

### Available targets

```bash
make lint                # Run golangci-lint
make build               # Build (runs lint first)
make test                # Run tests with race detector
make run                 # Quick start (set env vars first)
make e2e                 # Run end-to-end Hurl tests (starts server with isolated state)
make chaos               # Run a short deterministic chaos test (15s, seed=1)
make docker-build        # Build local Docker image (runs tests first)
make docker-build-multi  # Build multi-arch images via docker buildx
make release             # Tag, push, and publish multi-arch images to GHCR
```

### Testing

```bash
# Run all tests (race-enabled)
make test

# Run a specific test suite
go test -race ./queue/ -run TestScheduleAndClaim
go test -race ./api/ -run TestAdminEndpoints
```

### Chaos Tests

The chaos test is a standalone Go orchestrator that runs the real `http-queue` binary against isolated BadgerDB state, injects randomized worker and server faults, and audits invariants after the run.

Quick run:

```bash
make chaos
go run ./tests/chaos -duration=15s -seed=1 -report=./chaos-report.html
```

The full chaos testing guide lives in [docs/chaos-testing.md](docs/chaos-testing.md), including:

- run flags and examples
- HTML report generation
- file-backed artifacts: `events.jsonl`, `summary.json`, `report.html`
- counter semantics such as why `Double ACKs` is a scenario count, not automatically a failure
- reproducibility and debugging workflow
- invariant audit details

### End-to-End Tests

[![Hurl](https://img.shields.io/badge/-Hurl-FF5722?logo=hurl&logoColor=white)](https://hurl.dev)

The project includes a suite of Hurl-based end-to-end tests that exercise the full HTTP API against a running server with an isolated temporary BadgerDB instance.

#### Prerequisites

Install [Hurl](https://hurl.dev) (4.0+):

```bash
brew install hurl          # macOS
# or: https://hurl.dev/docs/installation.html
```

#### Test suite

| File | Coverage |
|------|----------|
| `001-happy-path.hurl` | Register worker → schedule job → claim → ack → empty queue |
| `002-nack-reclaim.hurl` | Claim → nack (requeue) → reclaim → ack |
| `003-multiple-workers-exclusive-claim.hurl` | Two workers; one claims, other gets 204; ownership preserved |
| `004-auth-and-validation.hurl` | Missing/wrong Basic Auth, invalid Bearer tokens, bad JSON body, invalid queue names (`:`, `/`), empty queue 204 |
| `005-ownership-and-deregister.hurl` | Cross-worker ack/nack rejection; deregister requeues reserved jobs |
| `006-timeout-and-max-attempts.hurl` | Visibility timeout auto-requeue; nack at `MAX_ATTEMPTS` moves to dead-letter |

#### Running

```bash
make e2e
```

Or invoke the runner directly:

```bash
./scripts/e2e-local.sh
```

#### How the local runner works

`scripts/e2e-local.sh` does the following:

1. **Finds a free port** using a ephemeral socket bind.
2. **Creates a temporary BadgerDB directory** (`mktemp -d /tmp/http-queue-e2e.XXXXXX`) — state is fully isolated per run and cleaned up on exit.
3. **Generates a unique `run_id`** (e.g. `e2e-1712345678`) so queue names in concurrent executions do not collide.
4. **Configures fast timings** for deterministic testing without long waits:

   | Variable | E2E value | Purpose |
   |----------|-----------|---------|
   | `ADMIN_USER` | `e2e-admin` | Fixed credentials for test requests |
   | `ADMIN_PASS` | `e2e-secret` | |
   | `VISIBILITY_TIMEOUT` | `2s` | Fast job expiry for timeout tests |
   | `WORKER_EXPIRY` | `5s` | Fast worker expiry for deregister tests |
   | `SWEEP_INTERVAL` | `1s` | Aggressive reconciliation sweep |
   | `MAX_ATTEMPTS` | `3` | Low threshold for dead-letter testing |
   | `LAST_SEEN_DEBOUNCE` | `100ms` | Minimal debounce for fast polling |

5. **Builds and starts the Go server** in the background (compiled binary for clean teardown).
6. **Waits for readiness** by polling `POST /workers` (expects `401` when the server is reachable — no health endpoint needed).
7. **Runs all `*.hurl` files** in `tests/e2e/` via `hurl --test`, passing `base_url` and `run_id` as variables.
8. **Cleans up** — kills the server process and removes the temporary BadgerDB directory, regardless of success or failure.

Each Hurl file uses `{{base_url}}` (e.g. `http://127.0.0.1:34821`) and `{{run_id}}` (e.g. `e2e-1712345678`) to stay isolated. The `run_id` is embedded in queue names like `{{run_id}}-happy`.

> **Note:** The test runner uses `VISIBILITY_TIMEOUT=2s` and `SWEEP_INTERVAL=1s`. The timeout-requeue test (`006-timeout-and-max-attempts.hurl`) waits up to 5s with retries, so these values keep the suite fast (~10–15s total).

#### Debugging E2E Tests

If a test fails, the runner prints Hurl's detailed diff output. You can also run a single test file interactively:

```bash
hurl --test --variable base_url=http://127.0.0.1:8080 --variable run_id=debug-01 tests/e2e/001-happy-path.hurl
```

To inspect server logs during a run, start the server manually and then run Hurl against it:

```bash
ADMIN_USER=e2e-admin ADMIN_PASS=e2e-secret BADGER_PATH=/tmp/hq-e2e-debug \
  VISIBILITY_TIMEOUT=2s WORKER_EXPIRY=5s SWEEP_INTERVAL=1s MAX_ATTEMPTS=3 \
  LAST_SEEN_DEBOUNCE=100ms \
  go run . &

hurl --test --variable base_url=http://127.0.0.1:8080 --variable run_id=debug-01 tests/e2e/*.hurl
```

## License

MIT
