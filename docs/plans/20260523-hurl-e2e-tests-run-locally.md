# Plan: Hurl E2E Tests Run Locally

## Overview
Add file-based Hurl end-to-end tests for the HTTP queue API and a local runner that starts the Go server with isolated temporary BadgerDB state. The suite will cover the core queue lifecycle plus important edge cases: auth failures, invalid input, exclusive claims, ownership enforcement, deregistration requeue, visibility timeout requeue, and max-attempt dead-letter behavior.

## Context
- Project is a Go HTTP service with `go run .` as the local entrypoint.
- Runtime config is environment-based: `ADMIN_USER`, `ADMIN_PASS`, `PORT`, `BADGER_PATH`, `VISIBILITY_TIMEOUT`, `WORKER_EXPIRY`, `SWEEP_INTERVAL`, `MAX_ATTEMPTS`, and `LAST_SEEN_DEBOUNCE`.
- Existing Go tests cover handlers and queue behavior in-process; Hurl tests should exercise the real HTTP server process.
- Existing Makefile has `lint`, `build`, `test`, and `run` targets.
- Queue semantics are claim-based: a pending job should be reserved by only one worker, and another worker must not receive the same job while it is reserved.
- There is no health endpoint, so readiness polling can use an existing route such as unauthenticated `POST /workers`, expecting `401` once the server is reachable.

## Edge Case Coverage
- Covered by this E2E plan:
  - Missing/wrong admin Basic Auth.
  - Missing/invalid worker Bearer token.
  - Invalid job JSON.
  - Invalid queue name containing `:`.
  - Empty queue returns `204 No Content`.
  - A reserved job cannot be claimed by another worker.
  - A worker cannot ack/nack a job owned by another worker.
  - Deregistering a worker requeues its reserved jobs.
  - Visibility timeout requeues an unacked job.
  - Nack at `MAX_ATTEMPTS` removes the job from normal claim flow.
- Not covered in Hurl E2E unless extra app support is added:
  - Direct BadgerDB storage invariants and reconciliation internals.
  - True concurrent race testing with simultaneous claims.
  - Badger value-log GC behavior.
  - Request-body size limit behavior for payloads over 1 MiB.

## Success Criteria
- [ ] `make e2e` starts the service locally, runs all Hurl files, and stops the service cleanly.
- [ ] `hurl --test --variable base_url=http://127.0.0.1:<port> tests/e2e/*.hurl` passes against a running local server when the required variables are supplied.
- [ ] `make test` continues to pass.
- [ ] E2E runs use a temporary `BADGER_PATH` and do not leave server processes or persistent test data behind.
- [ ] The E2E suite proves one queued job is not shared with two registered workers at the same time.
- [ ] The E2E suite includes edge coverage for auth, validation, ownership, deregistration, visibility timeout, and max-attempt behavior.

### Task 1: Add Local Hurl Runner

**Files:**
- Create: `scripts/e2e-local.sh`
- Modify: `Makefile`

- [x] Create `scripts/e2e-local.sh` that checks `hurl` is installed and exits with a clear message if not.
- [x] Have the script allocate a local test port, create a temporary BadgerDB directory, generate a unique `run_id`, export deterministic test env vars, and start `go run .` in the background.
- [x] Configure fast E2E timings with short `VISIBILITY_TIMEOUT`, `WORKER_EXPIRY`, and `SWEEP_INTERVAL`, plus deterministic `MAX_ATTEMPTS`.
- [x] Add readiness polling against the local server before running Hurl files.
- [x] Add `trap` cleanup to kill the server process and remove temporary BadgerDB data on success, failure, or interrupt.
- [x] Add a `make e2e` target that runs `scripts/e2e-local.sh`.

### Task 2: Add Happy Path Hurl Test

**Files:**
- Create: `tests/e2e/001-happy-path.hurl`

- [x] Register a worker through `POST /workers` using Basic Auth and capture `worker_id` and `token`.
- [x] Schedule a job through `POST /queues/{{run_id}}-happy/jobs` and capture the returned job ID.
- [x] Claim the job through `GET /queues/{{run_id}}-happy/next` using the captured bearer token.
- [x] Assert status codes, JSON fields, payload contents, queue name, and `attempts == 1`.
- [x] Ack the claimed job and assert the queue is empty on the next claim attempt with `204 No Content`.

### Task 3: Add Nack And Reclaim Hurl Test

**Files:**
- Create: `tests/e2e/002-nack-reclaim.hurl`

- [x] Register a worker and schedule a job in an isolated queue.
- [x] Claim the job and capture its ID.
- [x] Nack the job with `POST /jobs/{{job_id}}/nack` and assert `204`.
- [x] Claim the same job again and assert the ID matches the original job and `attempts == 2`.
- [x] Ack the reclaimed job and assert the queue returns `204 No Content` afterward.

### Task 4: Add Multiple Workers Exclusive Claim Test

**Files:**
- Create: `tests/e2e/003-multiple-workers-exclusive-claim.hurl`

- [x] Register two separate workers through `POST /workers` and capture both bearer tokens.
- [x] Schedule exactly one job through `POST /queues/{{run_id}}-multi-worker/jobs` and capture its job ID.
- [x] Claim the job with the first worker token and assert `200`, the captured job ID, the expected queue, payload, and `attempts == 1`.
- [x] Before acking or nacking the job, attempt to claim from the same queue with the second worker token and assert `204 No Content`.
- [x] Ack the job with the first worker token, then assert both workers receive `204 No Content` on subsequent claims from that queue.

### Task 5: Add Auth And Validation Edge Tests

**Files:**
- Create: `tests/e2e/004-auth-and-validation.hurl`

- [x] Assert admin endpoints return `401` without Basic Auth.
- [x] Assert admin endpoints return `401` with wrong Basic Auth credentials.
- [x] Assert worker endpoints return `401` without bearer auth.
- [x] Assert worker endpoints return `401` with an invalid bearer token.
- [x] Assert invalid job JSON returns `400` with a JSON error response.
- [x] Assert an invalid queue name containing `:` returns `400` with a JSON error response.

### Task 6: Add Ownership And Deregistration Edge Tests

**Files:**
- Create: `tests/e2e/005-ownership-and-deregister.hurl`

- [x] Register two workers and schedule one job in an isolated queue.
- [x] Claim the job with worker A and capture the job ID.
- [x] Assert worker B cannot ack worker A’s reserved job and receives `400`.
- [x] Assert worker B cannot nack worker A’s reserved job and receives `400`.
- [x] Deregister worker A through `DELETE /workers/{{worker_a_id}}` using Basic Auth.
- [x] Assert worker B can then claim the same job ID because deregistration requeued worker A’s reservation.

### Task 7: Add Visibility Timeout And Max Attempts Edge Tests

**Files:**
- Create: `tests/e2e/006-timeout-and-max-attempts.hurl`

- [x] Register a worker, schedule a job, claim it, and intentionally do not ack or nack it.
- [x] Wait long enough for the runner’s short `VISIBILITY_TIMEOUT` and `SWEEP_INTERVAL` to expire the reservation.
- [x] Claim again and assert the same job ID is returned with incremented attempts.
- [x] Nack or timeout the job until `MAX_ATTEMPTS` is reached.
- [x] Assert the job is no longer returned by `GET /queues/{{run_id}}-max-attempts/next`, proving it left the normal pending flow.

### Task 8: Document Local E2E Workflow

**Files:**
- Modify: `README.md`

- [ ] Add Hurl as an optional local E2E prerequisite.
- [ ] Document `make e2e` as the preferred local command.
- [ ] Document how to run Hurl files manually against an already-running server using `--variable base_url=...` and `--variable run_id=...`.
- [ ] Mention that the runner uses temporary BadgerDB data, test credentials, and short timeout settings.
- [ ] Mention that the E2E suite includes edge coverage for multiple-worker exclusive claim, auth, validation, ownership, deregistration requeue, visibility timeout, and max-attempt behavior.
