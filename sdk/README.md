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

## Status

This task only scaffolds the package boundary and validation flow. Generated API types, concrete clients, runner behavior, and React Native adapters are added in later tasks from the implementation plan.
