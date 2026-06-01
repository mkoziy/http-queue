import { describe, expect, it } from "bun:test";

import { createMemoryStateStore } from "../src/core/memory-state-store";
import type { CredentialProvider, PersistedTargetState } from "../src/core/types";
import { WorkerRunner } from "../src/worker/runner";
import { PERSISTED_TARGET_STATE_VERSION } from "../src/worker/state";

describe("WorkerRunner single-target", () => {
  it("polls an empty queue, persists credentials, and honors server next-poll hints", async () => {
    const clock = createControlledClock();
    const stateStore = createMemoryStateStore<PersistedTargetState>();
    const requests: string[] = [];
    const credentialProviderCalls: string[] = [];
    const runner = new WorkerRunner({
      target: {
        key: "primary",
        baseUrl: "https://queue.example",
        queues: ["orders"],
        fetch: createFetchStub((input, init) => {
          requests.push(`${init?.method} ${String(input)}`);
          return new Response(null, {
            status: 204,
            headers: {
              "X-Next-Poll-Seconds": "3",
            },
          });
        }),
        credentialProvider: createCredentialProvider((reason) => {
          credentialProviderCalls.push(reason);
          return { workerId: "worker-1", token: "token-1" };
        }),
      },
      stateStore,
      handleJob: async () => {
        throw new Error("did not expect a claimed job");
      },
      now: clock.now,
      sleep: clock.sleep,
    });

    await runner.start();
    await waitUntil(() => requests.length === 1);
    await waitUntil(() => clock.sleepers.length === 1);

    const persisted = await stateStore.load("primary");

    expect(credentialProviderCalls).toEqual(["missing"]);
    expect(requests).toEqual(["GET https://queue.example/queues/orders/next"]);
    expect(persisted).toEqual({
      version: PERSISTED_TARGET_STATE_VERSION,
      credentials: {
        workerId: "worker-1",
        token: "token-1",
      },
      nextPollByQueue: {
        orders: 3_000,
      },
      currentLease: null,
    });

    runner.pause();
    await waitUntil(() => runner.getStatus() === "paused");
    clock.advance(3_000);
    clock.releaseNext();
    await flushMicrotasks();

    expect(requests).toHaveLength(1);

    runner.resume();
    await waitUntil(() => runner.getStatus() === "running");
    await waitUntil(() => requests.length === 2);

    await runner.stop();
    expect(runner.getStatus()).toBe("stopped");
  });

  it("claims, handles, and acknowledges a job while clearing the persisted lease", async () => {
    const clock = createControlledClock();
    const stateStore = createMemoryStateStore<PersistedTargetState>();
    const handledJobs: string[] = [];
    const requests: string[] = [];
    const runner = new WorkerRunner({
      target: {
        key: "primary",
        baseUrl: "https://queue.example",
        queues: ["orders"],
        fetch: createFetchStub((input, init) => {
          requests.push(`${init?.method} ${String(input)}`);

          if (requests.length === 1) {
            return new Response(
              JSON.stringify({
                id: "job-1",
                queue: "orders",
                payload: { orderId: 42 },
                attempts: 1,
              }),
              {
                status: 200,
                headers: {
                  "content-type": "application/json",
                  "X-Next-Poll-Seconds": "4",
                },
              },
            );
          }

          if (String(input).endsWith("/ack")) {
            return new Response(null, { status: 204 });
          }

          return new Response(null, {
            status: 204,
            headers: {
              "X-Next-Poll-Seconds": "4",
            },
          });
        }),
        credentialProvider: createCredentialProvider(() => ({
          workerId: "worker-1",
          token: "token-1",
        })),
      },
      stateStore,
      handleJob: async (job) => {
        handledJobs.push(job.id);
      },
      now: clock.now,
      sleep: clock.sleep,
    });

    await runner.start();
    await waitUntil(() => requests.includes("POST https://queue.example/jobs/job-1/ack"));
    await waitUntil(async () => (await stateStore.load("primary"))?.currentLease === null);

    expect(handledJobs).toEqual(["job-1"]);
    expect(requests.slice(0, 2)).toEqual([
      "GET https://queue.example/queues/orders/next",
      "POST https://queue.example/jobs/job-1/ack",
    ]);

    const persisted = await stateStore.load("primary");
    expect(persisted?.currentLease).toBeNull();
    expect(persisted?.nextPollByQueue.orders).toBe(4_000);

    await runner.stop();
  });

  it("refreshes credentials once on 401 and retries the failed operation safely", async () => {
    const clock = createControlledClock();
    const stateStore = createMemoryStateStore<PersistedTargetState>();
    const credentialProviderCalls: string[] = [];
    const authorizations: Array<string | null> = [];
    const runner = new WorkerRunner({
      target: {
        key: "primary",
        baseUrl: "https://queue.example",
        queues: ["orders"],
        fetch: createFetchStub((input, init) => {
          const authorization = new Headers(init?.headers).get("authorization");
          authorizations.push(authorization);

          if (String(input).endsWith("/next")) {
            return new Response(
              JSON.stringify({
                id: "job-1",
                queue: "orders",
                payload: { orderId: 42 },
                attempts: 1,
              }),
              {
                status: 200,
                headers: {
                  "content-type": "application/json",
                  "X-Next-Poll-Seconds": "5",
                },
              },
            );
          }

          if (authorization === "Bearer token-1") {
            return new Response("unauthorized", { status: 401 });
          }

          return new Response(null, { status: 204 });
        }),
        credentialProvider: createCredentialProvider((reason) => {
          credentialProviderCalls.push(reason);
          return reason === "missing"
            ? { workerId: "worker-1", token: "token-1" }
            : { workerId: "worker-1", token: "token-2" };
        }),
      },
      stateStore,
      handleJob: async () => {},
      now: clock.now,
      sleep: clock.sleep,
    });

    await runner.start();
    await waitUntil(() => authorizations.includes("Bearer token-2"));
    await waitUntil(async () => (await stateStore.load("primary"))?.currentLease === null);

    expect(credentialProviderCalls).toEqual(["missing", "unauthorized"]);
    expect(authorizations.slice(0, 3)).toEqual([
      "Bearer token-1",
      "Bearer token-1",
      "Bearer token-2",
    ]);

    const persisted = await stateStore.load("primary");
    expect(persisted?.credentials?.token).toBe("token-2");
    expect(persisted?.currentLease).toBeNull();

    await runner.stop();
  });

  it("restores persisted credentials and next-poll state across restarts", async () => {
    const clock = createControlledClock(1_000);
    const stateStore = createMemoryStateStore<PersistedTargetState>();
    await stateStore.save("primary", {
      version: PERSISTED_TARGET_STATE_VERSION,
      credentials: {
        workerId: "worker-1",
        token: "token-restored",
      },
      nextPollByQueue: {
        orders: 6_000,
      },
      currentLease: {
        jobId: "job-stale",
        queue: "orders",
        claimedAt: "2026-06-01T10:00:00.000Z",
      },
    });

    const requests: string[] = [];
    const credentialProviderCalls: string[] = [];
    const runner = new WorkerRunner({
      target: {
        key: "primary",
        baseUrl: "https://queue.example",
        queues: ["orders"],
        fetch: createFetchStub((input, init) => {
          requests.push(`${init?.method} ${String(input)}`);
          return new Response(null, {
            status: 204,
            headers: {
              "X-Next-Poll-Seconds": "2",
            },
          });
        }),
        credentialProvider: createCredentialProvider((reason) => {
          credentialProviderCalls.push(reason);
          return { workerId: "worker-2", token: "fresh-token" };
        }),
      },
      stateStore,
      handleJob: async () => {},
      now: clock.now,
      sleep: clock.sleep,
    });

    await runner.start();
    await waitUntil(() => clock.sleepers.length === 1);

    expect(credentialProviderCalls).toEqual([]);
    expect(requests).toEqual([]);

    clock.advance(5_000);
    clock.releaseNext();
    await waitUntil(() => requests.length === 1);
    await waitUntil(async () => (await stateStore.load("primary"))?.nextPollByQueue.orders === 8_000);

    const persisted = await stateStore.load("primary");
    expect(persisted?.credentials?.token).toBe("token-restored");
    expect(persisted?.currentLease).toEqual({
      jobId: "job-stale",
      queue: "orders",
      claimedAt: "2026-06-01T10:00:00.000Z",
    });
    expect(persisted?.nextPollByQueue.orders).toBe(8_000);

    await runner.stop();
  });
});

function createCredentialProvider(
  getCredentials: (reason: "missing" | "unauthorized") => { workerId: string; token: string },
): CredentialProvider {
  return {
    getCredentials(context) {
      return Promise.resolve(getCredentials(context.reason));
    },
  };
}

function createFetchStub(
  handler: (input: RequestInfo | URL, init?: RequestInit) => Response | Promise<Response>,
) {
  return (input: RequestInfo | URL, init?: RequestInit) => Promise.resolve(handler(input, init));
}

function createControlledClock(initialNow = 0) {
  let now = initialNow;
  const sleepers: Array<{ delayMs: number; resolve: () => void }> = [];

  return {
    sleepers,
    now: () => now,
    sleep: (delayMs: number) =>
      new Promise<void>((resolve) => {
        sleepers.push({ delayMs, resolve });
      }),
    advance: (delayMs: number) => {
      now += delayMs;
    },
    releaseNext: () => {
      const sleeper = sleepers.shift();
      sleeper?.resolve();
    },
  };
}

async function waitUntil(condition: () => boolean | Promise<boolean>, attempts = 50) {
  for (let index = 0; index < attempts; index += 1) {
    if (await condition()) {
      return;
    }

    await flushMicrotasks();
  }

  throw new Error("Condition was not met in time");
}

async function flushMicrotasks() {
  await Promise.resolve();
  await Promise.resolve();
}
