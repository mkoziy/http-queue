# Per-Job TTL In Queue

## Overview
- Add per-job TTL support to the queue API using nullable integer seconds in `POST /queues/{queue}/jobs`.
- Expired jobs should be cleaned up by application logic, not Badger native TTL, so job records and queue indexes stay consistent.
- TTL applies to `pending` and `reserved` jobs only; `dead-letter` jobs remain stored for diagnostics.

## Context (from discovery)
- files/components involved: `api/jobs.go`, `queue/job.go`, `queue/sweep.go`, `queue/job_test.go`, `queue/sweep_test.go`, `api/worker_handlers_test.go`, `README.md`, `tests/e2e/`
- related patterns found: job lifecycle state lives in `queue/job.go`; reservation expiry and maintenance sweep live in `queue/sweep.go`; scheduling request parsing is in `api/jobs.go`
- dependencies identified: BadgerDB transaction semantics, existing pending/reserved/dead index invariants, current `visibility timeout` and `MAX_ATTEMPTS` behavior

## Development Approach
- **testing approach**: Regular
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional - they are a required part of the checklist
  - write unit tests for new functions/methods
  - write unit tests for modified functions/methods
  - add new test cases for new code paths
  - update existing test cases if behavior changes
  - tests cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** - no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run tests after each change
- backward compatibility is not a hard requirement for this work

## Testing Strategy
- **unit tests**: extend queue and API tests for TTL parsing, expiry checks, cleanup behavior, and interactions with `ack`, `nack`, and reservation sweeps
- **e2e tests**: add or update Hurl coverage for TTL request format, expired pending cleanup, and expired reserved cleanup behavior

## Progress Tracking
- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Solution Overview
- Add nullable `ttl` seconds to the schedule-job API and convert it to an absolute `expiresAt` timestamp at write time.
- Keep `expiresAt` in the job record as the single source of truth; only add a new index if implementation shows a real need during cleanup work.
- Enforce TTL in queue logic:
  - expired `pending` jobs are deleted instead of claimed
  - expired `reserved` jobs can still be `ack`ed successfully
  - expired `reserved` jobs are deleted on `nack` or reservation-timeout sweep instead of being re-queued
  - `dead` jobs ignore TTL and stay stored

## Technical Details
- request contract: `ttl` is `null`/omitted for no TTL, positive integer seconds for expiring jobs, `<= 0` is invalid
- storage contract: store absolute `expiresAt` on the job record rather than raw TTL duration
- helper logic:
  - centralize expiry checks with a helper like `isExpired(job, now)`
  - centralize atomic job deletion so record/index cleanup stays consistent
- cleanup points:
  - `ScheduleJob` computes `expiresAt`
  - `ClaimNextJob` skips and deletes expired pending jobs
  - `NackJob` deletes expired reserved jobs instead of requeue/dead-letter transition
  - reservation sweeper deletes expired reserved jobs instead of requeue
  - `AckJob` remains valid for already-claimed jobs even if wall-clock TTL elapsed

## What Goes Where
- **Implementation Steps** (`[ ]` checkboxes): code, tests, and docs in this repository
- **Post-Completion** (no checkboxes): optional client updates if any consumers want to start sending `ttl`

## Implementation Steps

### Task 1: Extend job model and scheduling API for nullable TTL seconds

**Files:**
- Modify: `queue/job.go`
- Modify: `api/jobs.go`
- Modify: `queue/job_test.go`
- Modify: `api/admin_handlers_test.go`

- [x] add `expiresAt` support to the `Job` model and update `ScheduleJob` to accept an optional absolute expiry
- [x] update `POST /queues/{queue}/jobs` request parsing to accept `ttl: null | integer-seconds` and reject invalid values
- [x] update schedule response only as needed for the chosen contract, keeping semantics explicit in code
- [x] write queue tests for jobs created with and without TTL, including stored `expiresAt`
- [x] write API tests for valid `ttl`, omitted/null `ttl`, and invalid `ttl` inputs
- [x] run targeted API and queue tests - must pass before next task

### Task 2: Enforce TTL during claim and manual job transitions

**Files:**
- Modify: `queue/job.go`
- Modify: `queue/job_test.go`
- Modify: `api/worker_handlers_test.go`

- [x] add shared expiry/deletion helpers for queue operations so expired-job cleanup is transactional and consistent
- [x] update `ClaimNextJob` to delete expired pending jobs and continue scanning for the next live job
- [x] update `NackJob` so expired reserved jobs are deleted instead of re-queued or moved to dead-letter
- [x] preserve current `AckJob` behavior so already-claimed jobs can still be acknowledged after TTL elapses
- [x] write queue tests for expired pending claim-skip, near-expiry claim success, expired reserved nack deletion, and ack-after-expiry success
- [x] write handler-level tests covering claim and ack/nack TTL edge behavior through HTTP
- [x] run targeted API and queue tests - must pass before next task

### Task 3: Enforce TTL in sweeper maintenance paths

**Files:**
- Modify: `queue/sweep.go`
- Modify: `queue/sweep_test.go`

- [x] update reservation expiry sweep to delete reserved jobs whose job TTL already elapsed instead of re-queueing them
- [x] review startup/periodic maintenance paths for expired-job cleanup and add an additional pending-job sweep only if necessary to meet the chosen semantics
- [x] avoid applying TTL cleanup to `dead-letter` jobs
- [x] write sweep tests for expired reserved job deletion and for dead-letter jobs remaining intact after TTL
- [x] write tests for any added pending-expiry sweep behavior if such a sweep is introduced
- [x] run targeted sweep tests - must pass before next task

### Task 4: Document and cover end-to-end TTL behavior

**Files:**
- Modify: `README.md`
- Create: `tests/e2e/007-job-ttl.hurl`
- Modify: `scripts/e2e-local.sh`

- [x] document the new `ttl` request field and exact runtime semantics for claim, ack, nack, and dead-letter behavior
- [x] add Hurl coverage for job creation with TTL, expired pending cleanup, and expired reserved cleanup behavior
- [x] wire the new e2e case into the local e2e flow if needed by the current harness
- [x] verify docs examples match the implemented JSON contract
- [x] run relevant e2e and targeted tests - must pass before next task

### Task 5: Verify acceptance criteria
- [x] verify `ttl` is nullable integer seconds in the scheduling API
- [x] verify expired pending jobs are deleted and never returned by claim
- [x] verify expired reserved jobs can still be acked but are deleted on nack or reservation-timeout cleanup
- [x] verify dead-letter jobs remain stored regardless of TTL
- [x] verify README and e2e coverage reflect the final behavior
