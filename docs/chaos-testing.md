# Chaos Testing

The chaos test is a standalone Go orchestrator in `tests/chaos` that builds and runs the real `http-queue` server against an isolated temporary BadgerDB directory, exercises the HTTP API under randomized load, injects failure scenarios, and audits the final database state for invariants.

It is meant for local debugging and regression detection against the real binary and persistence path, not just in-process unit behavior.

## Quick Start

Short deterministic run:

```bash
make chaos
```

Equivalent direct command:

```bash
go run ./tests/chaos -duration=15s -publishers=3 -workers=5 -seed=1
```

Generate a single-file HTML report:

```bash
go run ./tests/chaos -duration=15s -seed=1 -report=./chaos-report.html
```

Longer run with more concurrency and restart injection:

```bash
go run ./tests/chaos \
  -duration=30s \
  -publishers=4 \
  -workers=8 \
  -seed=123 \
  -report=./chaos-report.html \
  -queues=3 \
  -restart-probability=0.15 \
  -visibility-timeout=3s \
  -worker-expiry=5s \
  -sweep-interval=1s \
  -max-attempts=3
```

## Flags

| Flag | Default | Meaning |
|------|---------|---------|
| `-duration` | `30s` | Total run length |
| `-publishers` | `2` | Number of concurrent publisher goroutines |
| `-workers` | `4` | Number of concurrent worker goroutines |
| `-seed` | current time | RNG seed for reproducibility |
| `-queues` | `3` | Number of queue names used during the run |
| `-visibility-timeout` | `3s` | Server visibility timeout for the run |
| `-worker-expiry` | `5s` | Server worker expiry for the run |
| `-sweep-interval` | `1s` | Server sweeper interval for the run |
| `-max-attempts` | `3` | Max attempts before a job becomes dead-letter |
| `-restart-probability` | `0` | Probability of a restart event per controller tick |
| `-keep-artifacts` | `false` | Keep the temporary run directory after the run |
| `-report` | empty | Output path for the self-contained HTML report |

## What It Does

Each run:

1. Builds the real `http-queue` binary into a temporary directory.
2. Starts it with isolated admin credentials and isolated BadgerDB state.
3. Waits for readiness using the real HTTP surface.
4. Runs concurrent publisher goroutines with randomized queue and marker selection.
5. Runs concurrent worker goroutines that claim jobs and perform randomized actions.
6. Runs a controller goroutine that injects higher-level chaos events.
7. Stops the server cleanly after the duration.
8. Opens BadgerDB read-only and audits invariants.
9. Exits non-zero if invariant failures are found.

## Failure Scenarios

The chaos runner intentionally injects these behaviors:

- `ack`: normal success path
- `nack`: explicit requeue path
- `abandon`: claim without ack or nack, relying on visibility timeout
- `slow_ack`: ack after the visibility timeout has likely expired
- `double_ack`: ack the same job twice on purpose
- `worker_killed`: cancel a live worker and reuse its stale token in probes
- `burst_publish`: publish a burst of extra jobs
- `stale_token_probe`: attempt claim, ack, and nack using a stale worker token
- `server_restarted`: restart the real server process mid-run

These are scenario injections, not automatically defects.

## Understanding The HTML Report

The report is a self-contained single HTML file generated at shutdown. It is built from file-backed artifacts rather than from in-memory UI state.

The report includes:

- run metadata
- counters
- audit result
- repro command
- artifact paths
- filterable event timeline

### Counter Semantics

Some counters represent successful business outcomes:

- `Publishes`
- `Claims`
- `ACKs`
- `NACKs`
- `Restarts`

Some counters represent intentionally injected chaos scenarios:

- `Abandoned`
- `Slow ACKs`
- `Double ACKs`

This is important:

- `Double ACKs 3` means the runner executed the `double_ack` scenario three times.
- It does not mean the system is broken three times.
- The expected behavior for a double-ACK scenario is usually:
  - first ACK returns `204`
  - second ACK returns `404` or `409`

Similarly:

- `Slow ACKs` counts delayed ACK scenarios
- `Abandoned` counts intentional no-ACK/no-NACK claim scenarios

Treat `Invariant Fails` as the primary correctness signal. Scenario counts tell you what was exercised, not whether it failed.

## Artifacts

When `-report` is used, the run produces a small artifact pipeline:

- `events.jsonl`
- `summary.json`
- `report.html`

`events.jsonl` is the canonical machine-readable event stream. `summary.json` contains final run metadata, counters, audit status, and repro data. `report.html` is built from those files and embeds all CSS, JS, and data directly.

By default the temporary run directory is removed at the end of the run. If you want to inspect the raw artifacts, keep them:

```bash
go run ./tests/chaos -duration=10s -seed=1 -report=./chaos-report.html -keep-artifacts
```

The run logs the exact `tmp_dir` path.

## Reproducing Failures

Every run logs its seed. Re-run with the same seed to reproduce the same randomized choices:

```bash
go run ./tests/chaos -duration=30s -publishers=4 -workers=8 -seed=7392841056
```

For deep debugging:

1. Re-run with the failing seed.
2. Add `-keep-artifacts`.
3. Generate or preserve the HTML report.
4. Inspect the event timeline and audit section.
5. Inspect the preserved BadgerDB directory if needed.

## Invariant Audit

After the server stops, the chaos runner opens BadgerDB read-only and checks invariants over persisted jobs, queue indexes, worker records, and observed claim/ack behavior.

The audit is the main correctness gate. The run fails when invariant violations are detected, even if many injected scenarios were handled as expected.

Examples of what the audit validates:

- published jobs are either ACKed or still present in DB
- queue indexes point to real job records
- queue indexes match job status
- a job does not exist in multiple queue indexes at once
- reserved jobs reference real workers
- ACK records correspond to prior claims

## Local Workflow

For local iteration, the most useful pattern is:

1. run chaos with a fixed seed
2. generate the HTML report
3. inspect counters and timeline
4. if the audit fails, re-run the exact seed with `-keep-artifacts`

Recommended command:

```bash
go run ./tests/chaos -duration=15s -publishers=3 -workers=5 -seed=1 -report=./chaos-report.html
```
