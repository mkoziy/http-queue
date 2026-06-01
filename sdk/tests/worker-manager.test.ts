import { describe, expect, it } from "bun:test";

import type { CredentialProvider } from "../src/core/types";
import { createWorkerManager } from "../src/worker/manager";

describe("WorkerManager", () => {
  it("starts one isolated loop per target key and keeps timing independent", async () => {
    const clock = createControlledClock();
    const requests: string[] = [];
    const manager = createWorkerManager({
      targets: [
        createTarget("alpha", {
          baseUrl: "https://alpha.example",
          queues: ["orders"],
          fetch: createFetchStub((input, init) => {
            requests.push(`alpha:${init?.method} ${String(input)}`);
            return new Response(null, {
              status: 204,
              headers: {
                "X-Next-Poll-Seconds": "30",
              },
            });
          }),
        }),
        createTarget("beta", {
          baseUrl: "https://beta.example",
          queues: ["orders"],
          fetch: createFetchStub((input, init) => {
            requests.push(`beta:${init?.method} ${String(input)}`);
            return new Response(null, {
              status: 204,
              headers: {
                "X-Next-Poll-Seconds": "1",
              },
            });
          }),
        }),
      ],
      handleJob: async () => {},
      now: clock.now,
      sleep: clock.sleep,
    });

    await manager.start();
    await waitUntil(() => requests.length === 2);
    await waitUntil(() => clock.sleepers.length === 2);

    expect(requests).toEqual([
      "alpha:GET https://alpha.example/queues/orders/next",
      "beta:GET https://beta.example/queues/orders/next",
    ]);
    expect(manager.getStatus()).toEqual({
      alpha: "running",
      beta: "running",
    });

    clock.advance(1_000);
    clock.releaseNextByDelay(1_000);
    await waitUntil(() => requests.length === 3);

    expect(requests[2]).toBe("beta:GET https://beta.example/queues/orders/next");

    await manager.stop();
    expect(manager.getStatus()).toEqual({
      alpha: "stopped",
      beta: "stopped",
    });
  });

  it("keeps credential refresh isolated per target", async () => {
    const clock = createControlledClock();
    const alphaCredentialCalls: string[] = [];
    const betaCredentialCalls: string[] = [];
    const alphaAuthorizations: Array<string | null> = [];
    const betaAuthorizations: Array<string | null> = [];
    const manager = createWorkerManager({
      targets: [
        createTarget("alpha", {
          baseUrl: "https://alpha.example",
          queues: ["orders"],
          credentialProvider: createCredentialProvider((reason) => {
            alphaCredentialCalls.push(reason);
            return reason === "missing"
              ? { workerId: "worker-alpha", token: "alpha-1" }
              : { workerId: "worker-alpha", token: "alpha-2" };
          }),
          fetch: createFetchStub((_input, init) => {
            const authorization = new Headers(init?.headers).get("authorization");
            alphaAuthorizations.push(authorization);

            if (authorization === "Bearer alpha-1") {
              return new Response("unauthorized", { status: 401 });
            }

            return new Response(null, {
              status: 204,
              headers: {
                "X-Next-Poll-Seconds": "10",
              },
            });
          }),
        }),
        createTarget("beta", {
          baseUrl: "https://beta.example",
          queues: ["orders"],
          credentialProvider: createCredentialProvider((reason) => {
            betaCredentialCalls.push(reason);
            return { workerId: "worker-beta", token: "beta-1" };
          }),
          fetch: createFetchStub((_input, init) => {
            betaAuthorizations.push(new Headers(init?.headers).get("authorization"));
            return new Response(null, {
              status: 204,
              headers: {
                "X-Next-Poll-Seconds": "10",
              },
            });
          }),
        }),
      ],
      handleJob: async () => {},
      now: clock.now,
      sleep: clock.sleep,
    });

    await manager.start();
    await waitUntil(() => alphaAuthorizations.includes("Bearer alpha-2"));
    await waitUntil(() => betaAuthorizations.includes("Bearer beta-1"));

    expect(alphaCredentialCalls).toEqual(["missing", "unauthorized"]);
    expect(betaCredentialCalls).toEqual(["missing"]);
    expect(betaAuthorizations).toEqual(["Bearer beta-1"]);

    await manager.stop();
  });

  it("does not let a failure on one target block another target", async () => {
    const clock = createControlledClock();
    const requests: string[] = [];
    let alphaAttempts = 0;
    const manager = createWorkerManager({
      targets: [
        createTarget("alpha", {
          baseUrl: "https://alpha.example",
          queues: ["orders"],
          fetch: createFetchStub(() => {
            alphaAttempts += 1;
            throw new Error("alpha network down");
          }),
        }),
        createTarget("beta", {
          baseUrl: "https://beta.example",
          queues: ["orders"],
          fetch: createFetchStub((input, init) => {
            requests.push(`beta:${init?.method} ${String(input)}`);
            return new Response(null, {
              status: 204,
              headers: {
                "X-Next-Poll-Seconds": "5",
              },
            });
          }),
        }),
      ],
      handleJob: async () => {},
      now: clock.now,
      sleep: clock.sleep,
    });

    await manager.start();
    await waitUntil(() => alphaAttempts >= 1);
    await waitUntil(() => requests.length === 1);

    expect(requests).toEqual([
      "beta:GET https://beta.example/queues/orders/next",
    ]);

    await manager.stop();
  });
});

function createTarget(
  key: string,
  overrides: Partial<{
    baseUrl: string;
    queues: string[];
    credentialProvider: CredentialProvider;
    fetch: (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;
  }> = {},
) {
  return {
    key,
    baseUrl: overrides.baseUrl ?? `https://${key}.example`,
    queues: overrides.queues ?? ["orders"],
    credentialProvider: overrides.credentialProvider ?? createCredentialProvider(() => ({
      workerId: `worker-${key}`,
      token: `${key}-token`,
    })),
    fetch: overrides.fetch,
  };
}

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
    releaseNextByDelay: (delayMs: number) => {
      const index = sleepers.findIndex((sleeper) => sleeper.delayMs === delayMs);
      const [sleeper] = index >= 0 ? sleepers.splice(index, 1) : [undefined];
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
