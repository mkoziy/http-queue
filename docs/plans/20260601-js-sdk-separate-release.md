# JS SDK In Separate Release Folder

## Overview
- Build a JavaScript/TypeScript SDK for `http-queue` in a new top-level `sdk/` directory with its own package and release flow.
- Support two primary clients:
  - `admin` client for Node/Bun server-side usage
  - `worker` client with universal runtime support, including React Native persistence
- Keep generated API types and low-level transport thin; write worker orchestration, persistence, credential refresh, and logging by hand.
- Publish the SDK independently from the Go server so releases can move on separate cadences.

## Context (from discovery)
- The repo is currently Go-only; there is no existing JS workspace, `package.json`, or TypeScript tooling.
- The queue server already exposes a stable OpenAPI document in `openapi.yaml`.
- Worker auth is opaque bearer token based; the queue server does not know about external JWT/session auth.
- Worker polling already includes advisory `X-Next-Poll-Seconds`, and worker expiry / cleanup already exist server-side.
- Existing release automation is focused on the Go binary and container publishing.

## Development Approach
- **testing approach**: Regular
- Create the SDK as an isolated subproject under `sdk/` with its own package metadata, scripts, and publishing configuration.
- Use generated types from `../openapi.yaml`, but keep runtime logic handwritten.
- Start with a single-package SDK release, even if internal source code is split by module. Avoid a multi-package monorepo in v1 unless packaging pressure appears.
- Keep the first runner conservative:
  - one independent loop per target
  - one in-flight lease per target
  - per-queue `nextPoll` memory
  - credential refresh only on missing credentials or `401`
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task.
- **CRITICAL: all tests must pass before starting next task**.
- Preserve a clean boundary:
  - generated schema/types
  - low-level HTTP client
  - runner/state/credential orchestration
  - runtime-specific adapters

## Testing Strategy
- **unit tests**:
  - credential refresh behavior
  - per-queue `nextPoll` scheduling
  - retry/backoff behavior for network / `5xx`
  - no-refresh behavior for `204` and normal empty queues
  - target isolation for multi-endpoint support
  - state persistence and restoration
  - logger non-fatal behavior
- **integration tests**:
  - start local Go server
  - verify generated client compatibility against live endpoints
  - verify runner claims, `ack`, `nack`, and handles `401` with proxy-refreshed token
- **React Native adapter tests**:
  - storage adapter contract with mocked secure storage
  - state serialization/deserialization boundaries

## Progress Tracking
- Mark completed items with `[x]` immediately when done.
- Add newly discovered work with `➕`.
- Record scope changes and constraints directly in this plan file.
- Keep release-work items separate from code-work items.

## Solution Overview
- Create a new `sdk/` subproject with its own `package.json`, TS config, build, and publish pipeline.
- Generate OpenAPI-derived types from the root `openapi.yaml` into the SDK source tree.
- Expose two public surfaces:
  - `AdminClient` for Node/Bun
  - `WorkerClient` + `WorkerRunner` for universal runtimes
- Use a `CredentialProvider` abstraction so queue credentials can come from an external authenticated proxy.
- Use a `StateStore` abstraction for persistence; provide a React Native secure storage adapter for mobile.
- Use a structured callback-based logger interface with no-op default behavior.

## Technical Details
- Recommended top-level layout:
  - `sdk/package.json`
  - `sdk/tsconfig.json`
  - `sdk/src/generated/`
  - `sdk/src/core/`
  - `sdk/src/admin/`
  - `sdk/src/worker/`
  - `sdk/src/react-native/`
  - `sdk/tests/`
- Runtime assumptions:
  - target ESM-first output
  - keep fetch injectable instead of binding to a specific implementation
  - keep storage and logger injectable
- Core interfaces:
  - `CredentialProvider`
  - `StateStore<T>`
  - `Logger` / `LogHandler`
  - `FetchLike`
- Worker state per target:
  - `credentials`
  - `nextPollByQueue`
  - `currentLease`
  - `version`
- Target model:
  - one target = one queue API base URL + queue list + credential provider + isolated state key
- Release model:
  - publish from `sdk/`
  - version SDK independently from Go server
  - root repo keeps source of truth, but npm release metadata lives in `sdk/`

## What Goes Where
- **Implementation Steps**: all SDK code, tests, docs, examples, and release plumbing inside `sdk/` plus minimal root docs updates
- **Post-Completion**:
  - npm org/package ownership
  - registry token setup
  - consumer integration in external proxy service
  - optional CI publish job wiring

## Implementation Steps

### Task 1: Scaffold isolated SDK project

**Files:**
- Create: `sdk/package.json`
- Create: `sdk/tsconfig.json`
- Create: `sdk/tsconfig.build.json`
- Create: `sdk/.gitignore`
- Create: `sdk/README.md`
- Create: `sdk/src/index.ts`
- Create: `sdk/tests/`

- [x] choose package metadata and package name for standalone publish from `sdk/`
- [x] add TypeScript build, test, lint, and codegen scripts under `sdk/package.json`
- [x] configure output format and exports for Node/Bun + universal consumption
- [x] add base SDK README describing admin client, worker client, runner, and proxy credential flow
- [x] write smoke tests for package entrypoints and build artifacts
- [x] run SDK tests/build before task 2

### Task 2: Add OpenAPI type generation

**Files:**
- Create: `sdk/scripts/generate-types.*`
- Create: `sdk/src/generated/`
- Modify: `sdk/package.json`
- Modify: `sdk/README.md`

- [x] add OpenAPI type generation using root `openapi.yaml` as the source
- [x] generate stable TypeScript types into `sdk/src/generated/`
- [x] document how and when generated files are refreshed
- [x] write tests or checks to catch drift between generated output and committed SDK source expectations
- [x] run codegen and SDK tests before task 3

### Task 3: Implement shared core contracts and utilities

**Files:**
- Create: `sdk/src/core/types.ts`
- Create: `sdk/src/core/http.ts`
- Create: `sdk/src/core/errors.ts`
- Create: `sdk/src/core/logger.ts`
- Create: `sdk/src/core/state-store.ts`
- Create: `sdk/src/core/backoff.ts`
- Create: `sdk/tests/core/*.test.ts`

- [x] define `FetchLike`, request/response helpers, and normalized SDK error types
- [x] define `CredentialProvider`, `StateStore`, logger, target config, and persisted state types
- [x] implement structured logger wrapper with no-op default and callback adapter
- [x] implement network retry/backoff utilities separate from advisory next-poll scheduling
- [x] write unit tests for core error mapping and logger non-fatal behavior
- [x] write unit tests for backoff policy behavior
- [x] run SDK tests before task 4

### Task 4: Implement low-level admin and worker HTTP clients

**Files:**
- Create: `sdk/src/admin/client.ts`
- Create: `sdk/src/worker/client.ts`
- Modify: `sdk/src/index.ts`
- Create: `sdk/tests/admin-client.test.ts`
- Create: `sdk/tests/worker-client.test.ts`

- [ ] implement `AdminClient` methods for scheduling jobs, registering workers, and deregistering workers
- [ ] implement `WorkerClient` methods for claim-next, ack, and nack
- [ ] parse and expose `X-Next-Poll-Seconds` from worker claim responses
- [ ] keep auth strategy explicit: Basic Auth for admin, bearer token for worker
- [ ] write unit tests for success, auth failure, bad response, and header parsing cases
- [ ] run SDK tests before task 5

### Task 5: Implement persisted target state and state store adapters

**Files:**
- Create: `sdk/src/worker/state.ts`
- Create: `sdk/src/core/memory-state-store.ts`
- Create: `sdk/src/react-native/secure-state-store.ts`
- Create: `sdk/tests/state-store.test.ts`
- Create: `sdk/tests/react-native-state-store.test.ts`

- [ ] define versioned persisted target state with credentials, `nextPollByQueue`, and `currentLease`
- [ ] implement in-memory store for tests and server-side short-lived use
- [ ] implement React Native secure storage adapter contract for Android-safe token persistence
- [ ] decide whether all state lives in secure storage for v1 and document that choice
- [ ] write tests for serialization, missing state, corrupted state, and version mismatch handling
- [ ] run SDK tests before task 6

### Task 6: Implement single-target worker runner

**Files:**
- Create: `sdk/src/worker/runner.ts`
- Modify: `sdk/src/index.ts`
- Create: `sdk/tests/worker-runner.single-target.test.ts`

- [ ] implement runner lifecycle: `start`, `stop`, `pause`, `resume`
- [ ] load persisted credentials or request them from `CredentialProvider` when absent
- [ ] persist `nextPollByQueue` and lease state across restarts
- [ ] prioritize server advisory `X-Next-Poll-Seconds` over local polling heuristics
- [ ] refresh credentials once on `401`, then retry the failed operation safely
- [ ] write unit tests for empty queue polling, successful claim/ack flow, and `401` refresh flow
- [ ] write unit tests for pause/resume and state restoration
- [ ] run SDK tests before task 7

### Task 7: Add multi-queue scheduling within a target

**Files:**
- Modify: `sdk/src/worker/runner.ts`
- Create: `sdk/src/worker/scheduler.ts`
- Create: `sdk/tests/worker-runner.multi-queue.test.ts`

- [ ] implement round-robin scheduling across queues within one target
- [ ] honor `nextPoll` independently per queue instead of globally
- [ ] ensure a quiet queue does not block polling of active queues
- [ ] keep the first release single-concurrency per target to avoid lease complexity
- [ ] write tests for per-queue scheduling, fairness, and advisory header handling
- [ ] run SDK tests before task 8

### Task 8: Add multi-target orchestration

**Files:**
- Create: `sdk/src/worker/manager.ts`
- Modify: `sdk/src/worker/runner.ts`
- Modify: `sdk/src/index.ts`
- Create: `sdk/tests/worker-manager.test.ts`

- [ ] implement one isolated loop per target key
- [ ] isolate credentials, state, logging context, and retries per target
- [ ] ensure failure or `401` on one target does not block other targets
- [ ] expose a top-level factory for multi-target worker management
- [ ] write tests for target isolation, independent refresh, and independent timing behavior
- [ ] run SDK tests before task 9

### Task 9: Add integration tests against the local Go server

**Files:**
- Create: `sdk/tests/integration/*.test.ts`
- Create: `sdk/tests/helpers/*`
- Modify: `sdk/package.json`
- Modify: root `README.md` or `sdk/README.md`

- [ ] add test helpers to boot or connect to a local `http-queue` server
- [ ] verify admin client can create jobs and register workers against the real API
- [ ] verify worker client and runner can claim, ack, nack, and respect next-poll headers
- [ ] verify credential replacement path with proxy-style credential injection in tests
- [ ] document how to run SDK integration tests locally from `sdk/`
- [ ] run unit and integration tests before task 10

### Task 10: Add examples and consumer-facing documentation

**Files:**
- Create: `sdk/examples/node-admin/*`
- Create: `sdk/examples/node-worker/*`
- Create: `sdk/examples/react-native-worker/*`
- Modify: `sdk/README.md`

- [ ] add a Node/Bun admin usage example
- [ ] add a worker runner example using a proxy-backed `CredentialProvider`
- [ ] add a React Native example showing secure storage usage and lifecycle wiring
- [ ] document logging hooks, credential refresh, multi-target config, and queue scheduling semantics
- [ ] write doc checks or example smoke tests where practical
- [ ] run SDK tests/examples checks before task 11

### Task 11: Add standalone SDK release flow

**Files:**
- Modify: `sdk/package.json`
- Create: `sdk/.npmignore` or equivalent publish config
- Create: `sdk/CHANGELOG.md` or release notes template
- Create: `sdk/Makefile` or `sdk/scripts/release.*`
- Modify: root `README.md`

- [ ] define SDK versioning independent from Go server releases
- [ ] add publish scripts that run from `sdk/` only
- [ ] ensure generated files and build output are included correctly in published artifacts
- [ ] document release steps and required registry credentials
- [ ] write release smoke checks to verify package contents before publish
- [ ] run build/test/publish dry-run checks before task 12

### Task 12: Optional server-side follow-ups for SDK ergonomics

**Files:**
- Modify: `openapi.yaml`
- Modify: `README.md`
- Create/Modify: relevant Go tests if API docs or headers change

- [ ] review whether OpenAPI should document worker auth and `X-Next-Poll-Seconds` more explicitly for codegen consumers
- [ ] review whether any response shapes or error semantics need tightening for SDK stability
- [ ] add or update tests if server contract changes are made to support the SDK
- [ ] update docs to reflect any contract clarifications
- [ ] run Go tests affected by any server-side contract changes

## Recommended Decisions To Keep
- Keep `sdk/` as a single publishable package in v1, even if internal code is modular.
- Do not make the SDK aware of proxy JWT/session semantics; keep that in `CredentialProvider`.
- Do not deregister old workers from the SDK when credentials rotate; let proxy policy and server expiry handle cleanup.
- Do not parallelize job execution per target in the first release.
- Keep logging structured and callback-based, with no secrets in events.

## Risks And Watchpoints
- React Native secure storage libraries differ between Expo and bare RN; adapter boundaries must stay thin.
- Multiple queue endpoints require strict target key isolation to avoid token mixups.
- Generated OpenAPI clients can become awkward if runtime code depends on their shapes too directly; keep the abstraction boundary clean.
- If integration tests depend on live process orchestration, keep them deterministic and separate from fast unit tests.
