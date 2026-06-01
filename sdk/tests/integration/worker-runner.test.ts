import { afterAll, beforeAll, describe, expect, it, setDefaultTimeout } from "bun:test";

import { AdminClient } from "../../src/admin/client";
import { createMemoryStateStore } from "../../src/core/memory-state-store";
import type { PersistedTargetState } from "../../src/core/types";
import { WorkerRunner } from "../../src/worker/runner";
import type { LocalServerHandle } from "../helpers/local-server";
import { startLocalServer } from "../helpers/local-server";

describe("WorkerRunner integration", () => {
  setDefaultTimeout(30_000);

  let server: LocalServerHandle;
  let adminClient: AdminClient;

  beforeAll(async () => {
    server = await startLocalServer();
    adminClient = new AdminClient({
      baseUrl: server.baseUrl,
      username: server.adminUsername,
      password: server.adminPassword,
    });
  });

  afterAll(async () => {
    await server?.stop();
  });

  it("refreshes credentials through the provider and respects server next-poll hints", async () => {
    const queue = `sdk-runner-${Date.now()}`;
    const firstWorker = await adminClient.registerWorker();
    const secondWorker = await adminClient.registerWorker();
    const sleepCalls: number[] = [];
    const handledJobs: string[] = [];
    const credentialReasons: string[] = [];
    const stateStore = createMemoryStateStore<PersistedTargetState>();

    await adminClient.scheduleJob(queue, {
      payload: { integration: true, type: "runner" },
    });

    await adminClient.deregisterWorker(firstWorker.worker_id);

    const runner = new WorkerRunner({
      target: {
        key: "integration-target",
        baseUrl: server.baseUrl,
        queues: [queue],
        credentialProvider: {
          async getCredentials(context) {
            credentialReasons.push(context.reason);

            return context.reason === "missing"
              ? { workerId: firstWorker.worker_id, token: firstWorker.token }
              : { workerId: secondWorker.worker_id, token: secondWorker.token };
          },
        },
      },
      stateStore,
      sleep: async (delayMs) => {
        sleepCalls.push(delayMs);
        await new Promise<void>((resolve) => {
          setTimeout(resolve, 10);
        });
      },
      handleJob: async (job) => {
        handledJobs.push(job.id);
      },
    });

    await runner.start();
    await waitUntil(() => handledJobs.length === 1);
    await waitUntil(() => sleepCalls.length >= 1);
    await runner.stop();

    expect(credentialReasons).toEqual(["missing", "unauthorized"]);
    expect(handledJobs).toHaveLength(1);
    expect(sleepCalls.some((delayMs) => delayMs >= 1_000)).toBe(true);

    const persisted = await stateStore.load("integration-target");
    expect(persisted?.credentials?.workerId).toBe(secondWorker.worker_id);
    expect(persisted?.currentLease).toBeNull();
    expect(persisted?.nextPollByQueue[queue]).toBeGreaterThan(0);
  });
});

async function waitUntil(condition: () => boolean | Promise<boolean>, attempts = 100) {
  for (let index = 0; index < attempts; index += 1) {
    if (await condition()) {
      return;
    }

    await new Promise<void>((resolve) => {
      setTimeout(resolve, 20);
    });
  }

  throw new Error("Condition was not met in time");
}
