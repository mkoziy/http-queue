import { afterAll, beforeAll, describe, expect, it, setDefaultTimeout } from "bun:test";

import { AdminClient } from "../../src/admin/client";
import type { LocalServerHandle } from "../helpers/local-server";
import { startLocalServer } from "../helpers/local-server";

describe("AdminClient integration", () => {
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

  it("schedules jobs and registers workers against the real server", async () => {
    const scheduled = await adminClient.scheduleJob(`sdk-admin-${Date.now()}`, {
      payload: { integration: true, type: "admin" },
      ttl: 60,
    });

    expect(scheduled.queue).toContain("sdk-admin-");
    expect(scheduled.status).toBe("pending");
    expect(scheduled.id).toBeString();
    expect(scheduled.created).toBeString();
    expect(scheduled.ttl).toBe(60);

    const worker = await adminClient.registerWorker();

    expect(worker.worker_id).toBeString();
    expect(worker.token).toBeString();
  });
});
