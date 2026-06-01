import { afterAll, beforeAll, describe, expect, it, setDefaultTimeout } from "bun:test";

import { AdminClient } from "../../src/admin/client";
import { WorkerClient } from "../../src/worker/client";
import type { LocalServerHandle } from "../helpers/local-server";
import { startLocalServer } from "../helpers/local-server";

describe("WorkerClient integration", () => {
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

  it("claims, nacks, reclaims, acks, and exposes next-poll headers", async () => {
    const queue = `sdk-worker-${Date.now()}`;
    const worker = await adminClient.registerWorker();
    const client = new WorkerClient({
      baseUrl: server.baseUrl,
      token: worker.token,
    });

    await adminClient.scheduleJob(queue, {
      payload: { step: "first" },
    });

    const firstClaim = await client.claimNext(queue);
    expect(firstClaim.job?.queue).toBe(queue);
    expect(firstClaim.job?.attempts).toBe(1);
    expect(firstClaim.nextPollSeconds).toBeGreaterThanOrEqual(1);

    await client.nack(firstClaim.job!.id);

    const secondClaim = await client.claimNext(queue);
    expect(secondClaim.job?.id).toBe(firstClaim.job?.id);
    expect(secondClaim.job?.attempts).toBe(2);
    expect(secondClaim.nextPollSeconds).toBeGreaterThanOrEqual(1);

    await client.ack(secondClaim.job!.id);

    const emptyClaim = await client.claimNext(queue);
    expect(emptyClaim.job).toBeNull();
    expect(emptyClaim.nextPollSeconds).toBeGreaterThanOrEqual(1);
  });
});
