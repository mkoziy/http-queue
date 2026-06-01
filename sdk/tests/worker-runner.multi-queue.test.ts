import { describe, expect, it } from "bun:test";

import { createMemoryStateStore } from "../src/core/memory-state-store";
import type { CredentialProvider, PersistedTargetState } from "../src/core/types";
import { WorkerRunner } from "../src/worker/runner";

describe("WorkerRunner multi-queue scheduling", () => {
  it("polls ready queues in round-robin order", async () => {
    const clock = createControlledClock();
    const requests: string[] = [];
    const runner = new WorkerRunner({
      target: {
        key: "primary",
        baseUrl: "https://queue.example",
        queues: ["alpha", "beta", "gamma"],
        fetch: createFetchStub((input, init) => {
          requests.push(`${init?.method} ${String(input)}`);
          return new Response(null, {
            status: 204,
            headers: {
              "X-Next-Poll-Seconds": "1",
            },
          });
        }),
        credentialProvider: createCredentialProvider(() => ({
          workerId: "worker-1",
          token: "token-1",
        })),
      },
      handleJob: async () => {},
      now: clock.now,
      sleep: clock.sleep,
    });

    await runner.start();
    await waitUntil(() => requests.length === 3);
    expect(requests).toEqual([
      "GET https://queue.example/queues/alpha/next",
      "GET https://queue.example/queues/beta/next",
      "GET https://queue.example/queues/gamma/next",
    ]);

    await waitUntil(() => clock.sleepers.length === 1);
    clock.advance(1_000);
    clock.releaseNext();
    await waitUntil(() => requests.length === 4);

    expect(requests[3]).toBe("GET https://queue.example/queues/alpha/next");

    await runner.stop();
  });

  it("keeps next-poll timing independent so a quiet queue does not block an active queue", async () => {
    const clock = createControlledClock();
    const stateStore = createMemoryStateStore<PersistedTargetState>();
    const requests: string[] = [];
    const runner = new WorkerRunner({
      target: {
        key: "primary",
        baseUrl: "https://queue.example",
        queues: ["quiet", "active"],
        fetch: createFetchStub((input, init) => {
          const request = `${init?.method} ${String(input)}`;
          requests.push(request);

          return new Response(null, {
            status: 204,
            headers: {
              "X-Next-Poll-Seconds": request.includes("/quiet/") ? "30" : "1",
            },
          });
        }),
        credentialProvider: createCredentialProvider(() => ({
          workerId: "worker-1",
          token: "token-1",
        })),
      },
      stateStore,
      handleJob: async () => {},
      now: clock.now,
      sleep: clock.sleep,
    });

    await runner.start();
    await waitUntil(() => requests.length === 2);
    await waitUntil(() => clock.sleepers.length === 1);

    expect(requests).toEqual([
      "GET https://queue.example/queues/quiet/next",
      "GET https://queue.example/queues/active/next",
    ]);
    expect(clock.sleepers[0]?.delayMs).toBe(1_000);

    clock.advance(1_000);
    clock.releaseNext();
    await waitUntil(() => requests.length === 3);
    await waitUntil(async () => (await stateStore.load("primary"))?.nextPollByQueue.active === 2_000);

    expect(requests[2]).toBe("GET https://queue.example/queues/active/next");

    const persisted = await stateStore.load("primary");
    expect(persisted?.nextPollByQueue.quiet).toBe(30_000);
    expect(persisted?.nextPollByQueue.active).toBe(2_000);

    await runner.stop();
  });

  it("keeps single concurrency per target even when multiple queues are ready", async () => {
    const clock = createControlledClock();
    const requests: string[] = [];
    let resolveJob: (() => void) | null = null;
    const jobHandled = new Promise<void>((resolve) => {
      resolveJob = resolve;
    });

    const runner = new WorkerRunner({
      target: {
        key: "primary",
        baseUrl: "https://queue.example",
        queues: ["alpha", "beta"],
        fetch: createFetchStub((input, init) => {
          requests.push(`${init?.method} ${String(input)}`);

          if (requests.length === 1) {
            return new Response(
              JSON.stringify({
                id: "job-1",
                queue: "alpha",
                payload: { value: 1 },
                attempts: 1,
              }),
              {
                status: 200,
                headers: {
                  "content-type": "application/json",
                  "X-Next-Poll-Seconds": "0",
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
              "X-Next-Poll-Seconds": "5",
            },
          });
        }),
        credentialProvider: createCredentialProvider(() => ({
          workerId: "worker-1",
          token: "token-1",
        })),
      },
      handleJob: async () => {
        await jobHandled;
      },
      now: clock.now,
      sleep: clock.sleep,
    });

    await runner.start();
    await waitUntil(() => requests.length === 1);
    await flushMicrotasks();

    expect(requests).toEqual([
      "GET https://queue.example/queues/alpha/next",
    ]);

    resolveJob?.();
    await waitUntil(() => requests.includes("POST https://queue.example/jobs/job-1/ack"));
    await waitUntil(() => requests.length >= 3);

    expect(requests.slice(0, 3)).toEqual([
      "GET https://queue.example/queues/alpha/next",
      "POST https://queue.example/jobs/job-1/ack",
      "GET https://queue.example/queues/beta/next",
    ]);

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
