# http-queue SDK

`http-queue-sdk` is the standalone JavaScript and TypeScript package for working with the Go-based `http-queue` server from Node, Bun, and universal worker runtimes.

The package lives under `sdk/` so SDK releases can move independently from the Go server binary and container publishing flow.

## Surfaces

- `AdminClient` for server-side job scheduling and worker registration via HTTP Basic Auth
- `WorkerClient` for bearer-token polling, `ack`, and `nack`
- `WorkerRunner` for one target's queue polling, persistence, and credential refresh
- `WorkerManager` for multi-target orchestration with isolated loops per target key
- React Native secure-storage adapters for persisted worker state

## Credential flow

The SDK does not know about your application session, JWT, or user identity model. Worker credentials come from an injected `CredentialProvider`, which usually calls your own authenticated proxy or backend.

The normal flow is:

1. your app authenticates however it wants
2. your backend/proxy asks `http-queue` for worker credentials
3. the SDK receives opaque queue credentials and uses them as bearer tokens
4. if credentials are missing or the queue returns `401`, the SDK asks the provider for a fresh credential set and retries the failed worker operation once

Only the worker surfaces refresh credentials. `AdminClient` always uses explicit Basic Auth credentials.

## Credential refresh

`WorkerRunner` refreshes credentials in two cases only:

- no credentials are currently persisted for the target
- a worker operation returns `401`

After a `401`, the runner asks the `CredentialProvider` for a fresh credential set, updates persisted state, and retries the failed worker operation once. Empty queue responses (`204`) and successful worker operations do not trigger refreshes.

## Examples

The repository includes checked examples under `sdk/examples/`:

- `examples/node-admin/index.ts`: Node/Bun admin flow for scheduling jobs plus worker registration and deregistration
- `examples/node-worker/index.ts`: proxy-backed worker manager with structured logging and multi-target configuration
- `examples/react-native-worker/index.ts`: React Native-style secure storage and app lifecycle wiring around `WorkerRunner`

These examples are typechecked by `bun run examples:check` so the docs stay aligned with the exported SDK API.

### Node/Bun admin

See `examples/node-admin/index.ts` for the full example. The important shape is:

```ts
import { AdminClient } from "http-queue-sdk";

const client = new AdminClient({
  baseUrl: "http://localhost:8080",
  username: "admin",
  password: "secret",
});

await client.scheduleJob("orders", {
  payload: { orderId: "order-123", action: "ship" },
  ttl: 600,
});
```

### Proxy-backed worker

See `examples/node-worker/index.ts` for a full multi-target worker example. The `CredentialProvider` keeps application auth outside the SDK:

```ts
import { createWorkerManager } from "http-queue-sdk";

const manager = createWorkerManager({
  targets: [
    {
      key: "primary-us",
      baseUrl: "https://queue-us.example.com",
      queues: ["orders", "returns"],
      credentialProvider,
    },
  ],
  handleJob: async (job, context) => {
    console.log(job.id, context.queue);
  },
});

await manager.start();
```

### React Native worker

See `examples/react-native-worker/index.ts` for a complete example. The React Native adapter boundary stays intentionally thin:

```ts
import { WorkerRunner, createPersistedTargetStateSecureStore } from "http-queue-sdk";

const runner = new WorkerRunner({
  target,
  stateStore: createPersistedTargetStateSecureStore(secureStorageAdapter),
  handleJob,
});
```

Provide a `SecureStorageAdapter` backed by `expo-secure-store`, `react-native-keychain`, or another encrypted storage layer. Bind app lifecycle changes by calling `runner.resume()` when the app becomes active and `runner.pause()` when it moves to the background.

## Multi-target workers

Use `WorkerManager` when you need more than one queue server, region, or credential domain. It starts one `WorkerRunner` per target key and keeps credentials, persisted state, retries, and logging context isolated per target. A `401` or network failure on one target does not block the others.

## Queue scheduling

`WorkerRunner` keeps one polling loop per target and one in-flight lease per target in v1.

- queues within a target are scheduled in round-robin order
- `nextPollByQueue` is tracked independently per queue, so a quiet queue does not block an active queue
- the server's `X-Next-Poll-Seconds` header wins over local heuristics for the next poll time
- if every queue in a target is waiting, the runner sleeps until the earliest queue becomes eligible again

## Logging

Logging is callback-based and optional:

```ts
import { createLogger } from "http-queue-sdk";

const logger = createLogger((event) => {
  console.log(JSON.stringify(event));
});
```

Log handlers receive `{ level, message, fields }`. Logging failures are swallowed so observability hooks cannot break worker control flow. Use `target.logger` or pass `logger` into `WorkerRunner` / `WorkerManager` to record queue polling, retries, and credential refresh events.

## Persistence

Worker runtime state is versioned and includes:

- worker credentials
- per-queue `nextPollByQueue` timestamps
- the current in-flight lease, when present

For v1, all target state lives in a single secure-storage entry on React Native. That keeps the bearer token and scheduling metadata together inside the secure storage boundary instead of splitting them across multiple stores.

The SDK exposes:

- an in-memory `StateStore` for tests and short-lived server-side usage
- a React Native secure-storage adapter contract you can back with `expo-secure-store`, `react-native-keychain`, or a similar encrypted storage layer

Persisted state version mismatches are treated as a recoverable cache miss and cleared automatically by the React Native target-state store. Corrupted JSON still fails loudly so broken state is visible during development and integration.

## OpenAPI type generation

The SDK commits generated API types under `sdk/src/generated/` and treats the repository root `openapi.yaml` as the source of truth.

Refresh generated types whenever `openapi.yaml` changes or before starting work that depends on request/response shapes:

```bash
bun run codegen
```

Validate that committed generated files are current:

```bash
bun run codegen:check
```

The drift check regenerates the OpenAPI types into a temporary file and fails if that output differs from the committed `sdk/src/generated/openapi.ts`.

## Development

From `sdk/`:

```bash
bun install
bun run lint
bun run codegen
bun run examples:check
bun run test
```

`bun run test` builds the SDK, typechecks the examples, and runs the fast smoke and unit tests. Run the real-server integration suite separately:

```bash
bun run test:integration
```

The integration helper builds the local Go server, boots it with an isolated temporary BadgerDB path, and exercises the public SDK clients and runner against the live HTTP API.

## Releasing

The SDK is released independently from the Go server. Cut SDK versions from `sdk/package.json` and `sdk/CHANGELOG.md`; they do not need to match the repository's Git tags used for the Go binary and container images.

Required credentials:

- npm package ownership for `http-queue-sdk`
- an npm auth token available through `~/.npmrc` or `NPM_TOKEN`

Before publishing, update the SDK version and changelog entry from `sdk/`:

```bash
npm version 0.1.1 --no-git-tag-version
```

Run the full release gate:

```bash
bun run release:check
```

That command runs linting, OpenAPI drift verification, unit tests, example checks, and a package smoke check based on `npm pack --dry-run`.

Verify the publishable package without uploading anything:

```bash
bun run release:dry-run
```

Publish the SDK from `sdk/` only:

```bash
bun run release:publish
```

The published package is intentionally limited to built output and release docs:

- `dist/**`, including generated OpenAPI runtime and type files
- `README.md`
- `CHANGELOG.md`
