# Worker Next Poll Header

## Overview
- Add an advisory response header to `GET /queues/{queue}/next` telling workers how many seconds to wait before polling that queue again.
- Compute the suggested interval from the number of recently active workers for that specific queue so idle polling load is spread across the pool.
- Keep the feature operational-only: durable queue behavior stays unchanged, and state resets cleanly on process restart.

## Context (from discovery)
- Files/components involved: `config/config.go`, `api/workers.go`, `api/worker_handlers_test.go`, `openapi.yaml`, `README.md`, `queue/worker.go`, `queue/sweep.go`
- Related patterns found: worker activity already keeps in-memory `LastSeen` state in `queue/worker.go` to reduce Badger writes; `GET /queues/{queue}/next` currently returns `200` with JSON or `204` with no body; API behavior is documented in `README.md` and `openapi.yaml`
- Dependencies identified: new runtime dependency on `github.com/go-pkgz/expirable-cache` for per-queue active-worker TTL tracking

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
- maintain backward compatibility for existing clients that ignore the new header

## Testing Strategy
- **unit tests**: required for every task (see Development Approach above)
- **integration/API tests**: extend handler tests to assert the advisory header on both `200` and `204` responses and to verify multi-worker queue behavior
- **documentation verification**: update config and API docs in `README.md` and `openapi.yaml` to match code behavior and header naming exactly

## Progress Tracking
- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Solution Overview
- Introduce dedicated env-backed config for worker-next polling advice: base interval, min interval, max interval, and activity window.
- Track recent worker activity per queue in memory with `expirable-cache`, using worker ID as the cache key and `WORKER_NEXT_ACTIVITY_WINDOW` as TTL.
- On every `GET /queues/{queue}/next`, refresh the worker’s queue activity entry, lazily delete expired entries, count active workers for that queue, compute `ceil(base * sqrt(active_workers))`, clamp to min/max, and return the result as a header in seconds.
- Leave existing in-memory worker `LastSeen`/flush debounce logic unchanged; queue activity is separate operational state.

## Technical Details
- Config:
  - add new fields to `config.Config`
  - load durations from env vars:
    - `WORKER_NEXT_BASE_INTERVAL`
    - `WORKER_NEXT_MIN_INTERVAL`
    - `WORKER_NEXT_MAX_INTERVAL`
    - `WORKER_NEXT_ACTIVITY_WINDOW`
  - validate that intervals are positive and `min <= max`
- Runtime tracking:
  - add a queue-scoped activity tracker, likely in `queue/worker.go` or a small dedicated runtime file in `queue/`
  - keep `queue -> expirable cache(workerID -> struct{})`
  - call `DeleteExpired()` lazily during `next` requests
  - remove empty per-queue caches after cleanup when practical
- Header contract:
  - return the header on both `200 OK` and `204 No Content`
  - header value is an integer number of seconds
  - naming to match implementation and docs exactly
- Formula:
  - `active_workers = max(1, count_recent_workers_for_queue)`
  - `next_poll = ceil(base_interval * sqrt(active_workers))`
  - `next_poll = clamp(min_interval, max_interval, next_poll)`

## What Goes Where
- **Implementation Steps** (`[ ]` checkboxes): code, tests, dependency, and documentation changes inside this repo
- **Post-Completion** (no checkboxes): client adoption of the header and any rollout tuning of env vars in real deployments

## Implementation Steps

### Task 1: Add worker-next polling config

**Files:**
- Modify: `config/config.go`
- Modify: `config/config_test.go`

- [x] add config fields and env loading for worker-next polling intervals and activity window
- [x] validate positive durations and `WORKER_NEXT_MIN_INTERVAL <= WORKER_NEXT_MAX_INTERVAL`
- [x] choose and document defaults that preserve sensible behavior after restart and with a single worker
- [x] write tests covering env loading, defaults, and invalid configurations
- [x] run config tests and ensure they pass before task 2

### Task 2: Implement per-queue active-worker tracking and interval calculation

**Files:**
- Modify: `queue/worker.go` or create a focused runtime helper under `queue/`
- Modify: `go.mod`
- Modify: `go.sum`
- Create: `queue/<tracker>_test.go` or extend existing queue tests as appropriate

- [x] add `go-pkgz/expirable-cache` as a dependency for queue activity tracking
- [x] implement per-queue worker activity caches keyed by worker ID with TTL = `WORKER_NEXT_ACTIVITY_WINDOW`
- [x] lazily purge expired entries and count active workers per queue
- [x] implement the `ceil(base * sqrt(active_workers))` computation with min/max clamping and integer-second output
- [x] write tests for active worker counting, expiration behavior, single-worker fallback, and interval calculation
- [x] run queue tests and ensure they pass before task 3

### Task 3: Return the advisory header from `GET /queues/{queue}/next`

**Files:**
- Modify: `api/workers.go`
- Modify: `api/worker_handlers_test.go`

- [x] compute the next-poll hint inside `HandleClaimNextJob` before writing either `200` or `204`
- [x] set the chosen response header on successful claim and empty-queue responses
- [x] keep existing response body/status behavior unchanged aside from the new header
- [x] write/update handler tests for header presence on `200` and `204`, plus a multi-worker queue scenario
- [x] run API tests and ensure they pass before task 4

### Task 4: Document the header and tuning model

**Files:**
- Modify: `openapi.yaml`
- Modify: `README.md`

- [x] document the new header on `GET /queues/{queue}/next`, including that it is advisory and returned on both `200` and `204`
- [x] document the four new env vars and explain how to tune them, including the “1 request every 5 minutes for one worker” example
- [x] describe that active workers are counted per queue within the configured activity window and that the state resets on process restart
- [x] review examples and wording so docs match the implemented header name and units exactly
- [x] run any doc-linked checks that exist, or manually verify consistency if none exist

## Post-Completion
- Consumers can start honoring the advisory header in their worker loop when ready; existing workers remain compatible if they ignore it.
- Real deployment values for `WORKER_NEXT_*` should be tuned from observed idle queue latency and polling load rather than assumed globally.
