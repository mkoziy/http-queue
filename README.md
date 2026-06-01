# http-queue

[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![BadgerDB](https://img.shields.io/badge/Storage-BadgerDB-1F2937)](https://github.com/dgraph-io/badger)
[![License](https://img.shields.io/badge/License-MIT-111827)](#license)

Durable HTTP queue engine in Go, backed by [BadgerDB](https://github.com/dgraph-io/badger). No broker, no Redis, no Kafka, no external control plane. Just one binary, one embedded database, and a pull-based worker model.

Workers claim jobs by polling the API. The first worker to claim a pending job wins. If a worker disappears, the job becomes visible again after the visibility timeout.

## Why This Exists

`http-queue` is useful when you want queue semantics without operating separate infrastructure:

- durable job storage in-process via BadgerDB
- simple HTTP API for publishers and workers
- at-least-once delivery with ack / nack semantics
- automatic recovery for expired workers and abandoned jobs
- dead-letter handling after repeated failures

## Table of Contents

- [Architecture](#architecture)
- [Core Behavior](#core-behavior)
- [Quick Start](#quick-start)
- [API](#api)
- [Configuration](#configuration)
- [Development](#development)
- [Docker](#docker)
- [Storage Model](#storage-model)
- [Security](#security)
- [Project Layout](#project-layout)
- [Releasing](#releasing)
- [License](#license)

## Architecture

```text
┌──────────┐   POST /queues/{queue}/jobs   ┌────────────┐
│  Admin   │   POST /workers               │            │
│  Client  │   DELETE /workers/{id}        │ http-queue │
└──────────┘   ────────────────►           │   (Go)     │
                                           │            │
┌──────────┐   GET  /queues/{queue}/next   │  BadgerDB  │
│  Worker  │   POST /jobs/{id}/ack         │ (durable)  │
│  (poll)  │   POST /jobs/{id}/nack        │            │
└──────────┘   ◄────────────────           └────────────┘
```

## Core Behavior

- **Claim-based pull**: no shared consumer cursor; the first successful claimant gets the job.
- **Visibility timeout**: reserved jobs return to the queue if they are not acked in time.
- **Per-job TTL**: jobs can expire individually via the optional `ttl` field at schedule time.
- **Dead-letter queue**: jobs move to dead-letter after `MAX_ATTEMPTS`.
- **Worker expiry**: inactive workers are removed automatically and their jobs are re-queued.
- **Debounced `LastSeen` writes**: hot-path polling avoids excessive BadgerDB churn.
- **Reconciliation sweeper**: periodic repair pass cleans up crash-orphaned state.
- **BadgerDB value log GC**: background maintenance prevents log growth from running away.

## Quick Start

### Prerequisites

- Go 1.26+

### Build and run

```bash
make build
make test

ADMIN_USER=admin ADMIN_PASS=secret go run .
```

Or via `make run`:

```bash
ADMIN_USER=admin ADMIN_PASS=secret make run
```

### 60-second demo

Start the server:

```bash
ADMIN_USER=admin ADMIN_PASS=secret \
BADGER_PATH=/tmp/http-queue-demo \
go run .
```

Register a worker:

```bash
curl -u admin:secret -X POST http://localhost:8080/workers
```

Example response:

```json
{
  "worker_id": "01JXXXXXXX...",
  "token": "abc123..._base64url_token..."
}
```

The bearer token is returned only once. Store it in worker configuration.

Schedule a job:

```bash
curl -u admin:secret -X POST http://localhost:8080/queues/orders/jobs \
  -H "Content-Type: application/json" \
  -d '{"payload": {"orderId": 42, "action": "process"}, "ttl": 600}'
```

Example response:

```json
{
  "id": "01JXXXXXXX...",
  "queue": "orders",
  "status": "pending",
  "created": "2026-05-23T00:00:00Z",
  "ttl": 600
}
```

Claim the job:

```bash
curl -H "Authorization: Bearer YOUR_WORKER_TOKEN" \
  http://localhost:8080/queues/orders/next
```

Ack the job:

```bash
curl -H "Authorization: Bearer YOUR_WORKER_TOKEN" \
  -X POST http://localhost:8080/jobs/01JXXXXXXX.../ack
```

Nack the job:

```bash
curl -H "Authorization: Bearer YOUR_WORKER_TOKEN" \
  -X POST http://localhost:8080/jobs/01JXXXXXXX.../nack
```

If the queue is empty, `GET /queues/{queue}/next` returns `204 No Content`. If a job reaches `MAX_ATTEMPTS`, `nack` moves it to dead-letter instead of re-queueing it.

## API

OpenAPI spec: [openapi.yaml](./openapi.yaml)

### Admin endpoints

Uses HTTP Basic Auth with `ADMIN_USER` and `ADMIN_PASS`.

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/queues/{queue}/jobs` | Schedule a new job with optional `ttl` seconds |
| `POST` | `/workers` | Register a new worker and return its bearer token |
| `DELETE` | `/workers/{id}` | Deregister a worker and re-queue any reserved jobs |

### Worker endpoints

Uses a bearer token returned at worker registration time.

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/queues/{queue}/next` | Claim the next pending job |
| `POST` | `/jobs/{id}/ack` | Mark a job complete |
| `POST` | `/jobs/{id}/nack` | Re-queue the job or move it to dead-letter |

### Typical worker loop

1. `POST /workers`
2. `GET /queues/{queue}/next`
3. Process the payload
4. `POST /jobs/{id}/ack` on success
5. `POST /jobs/{id}/nack` on failure

### Job TTL semantics

`POST /queues/{queue}/jobs` accepts an optional `ttl` field:

```json
{
  "payload": {"orderId": 42},
  "ttl": 600
}
```

- `ttl` may be omitted or set to `null` for no expiry
- `ttl` must be a positive integer number of seconds when provided
- expired `pending` jobs are deleted and never returned by `GET /queues/{queue}/next`
- if a job was already claimed, `POST /jobs/{id}/ack` still succeeds even if wall-clock TTL has elapsed
- if a claimed job has expired, `POST /jobs/{id}/nack` deletes it instead of re-queueing it
- if a claimed job hits visibility timeout after its TTL elapsed, the sweeper deletes it instead of re-queueing it
- jobs already moved to dead-letter remain stored even if their TTL is in the past

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `PORT` | `8080` | HTTP listen port |
| `ADMIN_USER` | required | Admin Basic Auth username |
| `ADMIN_PASS` | required | Admin Basic Auth password |
| `BADGER_PATH` | `/tmp/http-queue` | BadgerDB data directory |
| `VISIBILITY_TIMEOUT` | `30s` | Reservation window before auto-requeue |
| `WORKER_EXPIRY` | `5m` | How long a worker can stop polling before expiry |
| `SWEEP_INTERVAL` | `30s` | Sweep cadence for expiry and reconciliation |
| `MAX_ATTEMPTS` | `3` | Dead-letter threshold |
| `LAST_SEEN_DEBOUNCE` | `30s` | Minimum interval between persisted `LastSeen` updates |

## Development

### Common targets

```bash
make lint
make build
make test
make run
make e2e
make chaos
make docker-build
make docker-build-multi
make release VERSION=0.1.1
```

### Unit and package tests

```bash
make test
go test -race ./queue/ -run TestScheduleAndClaim
go test -race ./api/ -run TestAdminEndpoints
```

### End-to-end tests

[![Hurl](https://img.shields.io/badge/Hurl-E2E-FF5722?logoColor=white)](https://hurl.dev)

The E2E suite exercises the live HTTP API against an isolated temporary BadgerDB directory.

| File | Coverage |
| --- | --- |
| `001-happy-path.hurl` | Register worker, schedule job, claim, ack, empty queue |
| `002-nack-reclaim.hurl` | Nack and reclaim flow |
| `003-multiple-workers-exclusive-claim.hurl` | Exclusive claim behavior across workers |
| `004-auth-and-validation.hurl` | Auth failures, invalid tokens, bad JSON, invalid queue names |
| `005-ownership-and-deregister.hurl` | Ownership enforcement and deregister requeueing |
| `006-timeout-and-max-attempts.hurl` | Visibility timeout and dead-letter threshold |

Run the full suite:

```bash
make e2e
```

Or run the local wrapper directly:

```bash
./scripts/e2e-local.sh
```

The wrapper:

- picks a free port
- creates an isolated temporary BadgerDB directory
- uses a unique `run_id` to avoid queue collisions
- starts the compiled server in the background
- waits for readiness by probing `POST /workers`
- runs all files in `tests/e2e/`
- cleans up state and the server process on exit

Install Hurl first:

```bash
brew install hurl
```

To debug a single test:

```bash
hurl --test \
  --variable base_url=http://127.0.0.1:8080 \
  --variable run_id=debug-01 \
  tests/e2e/001-happy-path.hurl
```

### Chaos testing

The chaos runner launches the real binary against isolated state, injects randomized failures, and audits invariants after the run.

Quick run:

```bash
make chaos
go run ./tests/chaos -duration=15s -seed=1 -report=./chaos-report.html
```

Full guide: [docs/chaos-testing.md](docs/chaos-testing.md)

## Docker

The multi-stage [Dockerfile](Dockerfile) builds a statically linked binary into a minimal Alpine runtime image. The container runs as a non-root user.

Build locally:

```bash
make docker-build
make docker-build IMAGE=my-registry/http-queue VERSION=1.0.0
```

Run the container:

```bash
docker run --rm \
  -e ADMIN_USER=admin \
  -e ADMIN_PASS=secret \
  -e BADGER_PATH=/data \
  -v http-queue-data:/data \
  -p 8080:8080 \
  ghcr.io/mkoziy/http-queue:latest
```

Mount a volume for `BADGER_PATH` if you want queue state to survive container restarts.

For multi-arch builds:

```bash
make docker-build-multi
```

This target pushes images for `linux/amd64` and `linux/arm64` by default, so authenticate with your registry first.

## Storage Model

State is stored in BadgerDB with a compact key layout:

```text
job:{ulid}                        -> JSON: {queue, payload, status, workerID, createdAt, expiresAt, attempts}
queue:{queue}:pending:{ulid}      -> "" (index only)
queue:{queue}:reserved:{ulid}     -> expiry-unix-timestamp
queue:{queue}:dead:{ulid}         -> "" (dead-letter index)
worker:{worker-id}                -> JSON: {tokenHash, lastSeen, registeredAt}
workertoken:{sha256-hex}          -> worker-id
```

### Invariants

- every `queue:*:pending:{ulid}` key must have a matching `job:{ulid}` with `status=pending`
- every `queue:*:reserved:{ulid}` key must have a matching `job:{ulid}` with `status=reserved`
- the sweeper reconciles index keys against job records after crashes or partial failures
- queue names cannot contain `:`

## Security

- admin credentials are read from environment variables and kept only in process memory
- worker bearer tokens are generated from `crypto/rand`, returned once, and stored only as SHA-256 hashes
- token comparison uses constant-time comparison
- auth failures return a generic `401 Unauthorized`

## Project Layout

```text
.
├── main.go
├── api/
│   ├── jobs.go
│   ├── middleware.go
│   ├── router.go
│   └── workers.go
├── config/
│   └── config.go
├── db/
│   └── db.go
├── queue/
│   ├── batch.go
│   ├── job.go
│   ├── sweep.go
│   └── worker.go
├── tests/
│   ├── chaos/
│   └── e2e/
├── token/
│   └── token.go
├── Dockerfile
└── Makefile
```

## Releasing

Releases publish:

- a Git tag
- a GitHub release
- multi-architecture images to GHCR

Authenticate first:

```bash
echo $GITHUB_TOKEN | docker login ghcr.io -u mkoziy --password-stdin
gh auth login
```

Create a release:

```bash
make release VERSION=0.1.1
```

The release target:

1. runs race-enabled tests
2. verifies a clean working tree on `main`
3. checks that the tag does not already exist locally or remotely
4. builds and pushes multi-arch images
5. creates and pushes an annotated git tag
6. creates a GitHub release with notes from the previous tag diff

Use semantic versioning: `X.Y.Z`.

## License

MIT
