# http-queue SDK

`http-queue-sdk` is the standalone JavaScript and TypeScript package for working with the Go-based `http-queue` server from Node, Bun, and universal worker runtimes.

The package is scaffolded as an isolated release unit under `sdk/` so SDK versions can move independently from the server binary and container releases.

## Planned surfaces

- `AdminClient` for server-side job scheduling and worker registration via HTTP Basic Auth
- `WorkerClient` for bearer-token polling, `ack`, and `nack`
- `WorkerRunner` for queue polling, next-poll scheduling, persistence, and credential refresh orchestration
- React Native state persistence adapters for mobile worker runtimes

## Credential flow

The SDK itself does not know about your application session or JWT model. Worker credentials are expected to come from an injected `CredentialProvider` that can call your own authenticated proxy or backend.

That separation keeps the queue contract narrow:

1. your app authenticates however it wants
2. your backend/proxy asks `http-queue` for worker credentials
3. the SDK receives opaque queue credentials and uses them as bearer tokens
4. when credentials expire or the queue returns `401`, the SDK asks the provider for a fresh credential set

## Development

From `sdk/`:

```bash
bun install
bun run lint
bun run codegen
bun test
```

`bun test` runs the TypeScript build first, then executes smoke tests that verify the public entrypoint and the built package artifacts.

## OpenAPI type generation

The SDK commits generated API types under `sdk/src/generated/` and treats the repository root `openapi.yaml` as the single source of truth.

Refresh generated types whenever `openapi.yaml` changes or before starting work that depends on request/response shapes:

```bash
bun run codegen
```

Validate that committed generated files are current:

```bash
bun run codegen:check
```

The drift check regenerates the OpenAPI types into a temporary file and fails if that output differs from the committed `sdk/src/generated/openapi.ts`.

## Persistence

Worker runtime state is versioned and currently includes:

- worker credentials
- per-queue `nextPollByQueue` timestamps
- the current in-flight lease, when present

For v1, all of that target state lives in a single secure-storage entry on React Native. That keeps the bearer token and the scheduling metadata together inside the secure storage boundary instead of splitting credentials into one store and queue state into another.

The SDK exposes:

- an in-memory `StateStore` for tests and short-lived server-side usage
- a React Native secure-storage adapter contract you can back with `expo-secure-store`, `react-native-keychain`, or a similar encrypted storage layer

Persisted state version mismatches are treated as a recoverable cache miss and cleared automatically by the React Native target-state store. Corrupted JSON still fails loudly so broken state is visible during development and integration.

## Status

This task only scaffolds the package boundary and validation flow. Generated API types, concrete clients, runner behavior, and React Native adapters are added in later tasks from the implementation plan.
