# Plan: Fix Startup Sweep and Batched Maintenance

## Overview
Fix the review comments by preventing startup from expiring durable workers before they can reconnect, and by ensuring worker deregistration and expired-reservation cleanup never rewrite unbounded numbers of Badger keys in one transaction. The approach is to keep startup reconciliation/reservation expiry, split maintenance writes into bounded batches, and add regression tests for restart and large-maintenance scenarios.

## Context
- Project is a Go 1.26 HTTP queue backed by BadgerDB v4.9.1.
- Relevant storage invariants are documented in `README.md`: job records must match pending/reserved/dead queue indexes.
- Current maintenance paths are in `queue/sweep.go` and `queue/worker.go`.
- Verification commands used by the project are `go test -race ./...` and `make lint`.

## Success Criteria
- [ ] Startup sweeper does not delete workers whose durable `LastSeen` is older than `WORKER_EXPIRY` until at least the first scheduled sweep tick.
- [ ] `DeregisterWorker` re-queues large numbers of worker-owned reserved jobs through bounded Badger write transactions.
- [ ] Expired reservation cleanup processes large backlogs through bounded Badger write transactions and leaves no expired reservations stuck because of transaction size.
- [ ] `go test -race ./queue` passes.
- [ ] `go test -race ./...` passes.
- [ ] `make lint` passes, if `golangci-lint` is installed in the environment.

### Task 1: Protect Workers During Startup Sweep
**Files:**
- Modify: `queue/sweep.go`
- Modify: `queue/sweep_test.go`

- [x] Ensure `Sweeper.Start` performs only startup-safe maintenance immediately: expire reservations and reconciliation, but not `expireWorkers`.
- [x] Keep normal periodic ticks calling the full `sweep()` path, including worker expiry.
- [x] Add a regression test that creates a persisted worker with stale `LastSeen`, starts the sweeper with a long interval, and verifies the worker record and token index still exist after the immediate startup pass.
- [x] Add/adjust test assertions showing expired reservations are still handled by the immediate startup pass.

### Task 2: Add Shared Batching Helpers
**Files:**
- Modify: `queue/sweep.go`
- Modify: `queue/worker.go`

- [ ] Introduce a small unexported maintenance batch size constant, e.g. `maintenanceBatchSize`, used by both worker deregistration and expired-reservation sweeping.
- [ ] Add an unexported helper for slicing collected refs into bounded batches to avoid duplicating batching logic.
- [ ] Keep batch helpers package-private and simple so they do not affect the public queue API.
- [ ] Ensure all new code is gofmt-compatible and follows existing error wrapping style.

### Task 3: Batch Worker Deregistration Requeue Writes
**Files:**
- Modify: `queue/worker.go`
- Modify: `queue/worker_test.go`

- [ ] Refactor `DeregisterWorker` so it first loads the worker/token metadata, then collects worker-owned reserved job refs in a read transaction.
- [ ] Re-queue owned reservations in bounded write transactions, re-checking each job still exists, is still reserved, and is still owned by the worker before mutating it.
- [ ] Delete the worker record and token reverse index only after all reservation batches complete successfully, so a failed partial deregistration remains retryable.
- [ ] Preserve cleanup of `workerLastSeen` and `flush:<id>` only after successful deregistration.
- [ ] Add regression tests covering deregistration of many reserved jobs and confirming unrelated workers’ reservations remain untouched.

### Task 4: Batch Expired Reservation Processing
**Files:**
- Modify: `queue/sweep.go`
- Modify: `queue/sweep_test.go`

- [ ] Refactor `expireReservations` to collect expired reservation refs in a read transaction, then process refs in bounded write batches.
- [ ] Inside each batch write transaction, re-check the reserved index still exists and is still expired before updating the job.
- [ ] Preserve existing behavior for missing job records, malformed records, re-queue vs dead-letter decisions, and log messages.
- [ ] Ensure errors from one batch are logged without preventing later batches from being attempted when safe.
- [ ] Add regression tests for many expired reservations, including both re-queued and dead-lettered jobs.

### Task 5: Verify Invariants and Race Safety
**Files:**
- Modify: `queue/sweep_test.go`
- Modify: `queue/worker_test.go`

- [ ] Add invariant checks after batched operations: no stale reserved indexes, expected pending/dead indexes exist, and job `WorkerID` is cleared.
- [ ] Add or update race-oriented tests to verify concurrent ack/sweep and deregister/sweep outcomes remain consistent.
- [ ] Run `go test -race ./queue` and fix any failing queue tests.
- [ ] Run `go test -race ./...` and fix any package-wide regressions.
- [ ] Run `make lint` if available and address lint findings.
