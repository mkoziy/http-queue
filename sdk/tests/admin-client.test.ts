import { describe, expect, it } from "bun:test";

import { AdminClient } from "../src/admin/client";
import type { FetchLike } from "../src/core/types";
import { HttpQueueSdkError } from "../src/core/errors";

describe("AdminClient", () => {
  it("schedules jobs and sends basic auth", async () => {
    const client = new AdminClient({
      baseUrl: "https://queue.example",
      username: "admin",
      password: "secret",
      fetch: createFetchStub((input, init) => {
        expect(String(input)).toBe("https://queue.example/queues/orders/jobs");
        expect(init?.method).toBe("POST");
        expect(readHeader(init, "authorization")).toBe(`Basic ${btoa("admin:secret")}`);
        expect(readHeader(init, "content-type")).toBe("application/json");
        expect(init?.body).toBe(JSON.stringify({ payload: { orderId: 42 }, ttl: 60 }));

        return jsonResponse(
          {
            id: "job-1",
            queue: "orders",
            status: "pending",
            created: "2026-06-01T10:00:00Z",
            ttl: 60,
          },
          201,
        );
      }),
    });

    await expect(
      client.scheduleJob("orders", { payload: { orderId: 42 }, ttl: 60 }),
    ).resolves.toEqual({
      id: "job-1",
      queue: "orders",
      status: "pending",
      created: "2026-06-01T10:00:00Z",
      ttl: 60,
    });
  });

  it("registers workers and returns typed credentials", async () => {
    const client = new AdminClient({
      baseUrl: "https://queue.example/api",
      username: "admin",
      password: "secret",
      fetch: createFetchStub((_input, init) => {
        expect(init?.method).toBe("POST");
        expect(readHeader(init, "authorization")).toBe(`Basic ${btoa("admin:secret")}`);

        return jsonResponse({ worker_id: "worker-1", token: "worker-token" }, 201);
      }),
    });

    await expect(client.registerWorker()).resolves.toEqual({
      worker_id: "worker-1",
      token: "worker-token",
    });
  });

  it("maps auth failures and supports deregistration", async () => {
    const successClient = new AdminClient({
      baseUrl: "https://queue.example",
      username: "admin",
      password: "secret",
      fetch: createFetchStub((input, init) => {
        expect(String(input)).toBe("https://queue.example/workers/worker-1");
        expect(init?.method).toBe("DELETE");
        return new Response(null, { status: 204 });
      }),
    });

    await expect(successClient.deregisterWorker("worker-1")).resolves.toBeUndefined();

    const failingClient = new AdminClient({
      baseUrl: "https://queue.example",
      username: "admin",
      password: "secret",
      fetch: createFetchStub(() => new Response("unauthorized", { status: 401 })),
    });

    await expect(failingClient.registerWorker()).rejects.toMatchObject<HttpQueueSdkError>({
      code: "AUTH_ERROR",
      statusCode: 401,
    });
  });

  it("rejects invalid JSON responses", async () => {
    const client = new AdminClient({
      baseUrl: "https://queue.example",
      username: "admin",
      password: "secret",
      fetch: createFetchStub(() => new Response("not json", { status: 201 })),
    });

    await expect(client.registerWorker()).rejects.toMatchObject<HttpQueueSdkError>({
      code: "INVALID_RESPONSE",
    });
  });
});

function createFetchStub(handler: NonNullable<FetchLike>): FetchLike {
  return ((input, init) => handler(input, init)) as FetchLike;
}

function jsonResponse(body: unknown, status: number) {
  return new Response(JSON.stringify(body), {
    status,
    headers: {
      "content-type": "application/json",
    },
  });
}

function readHeader(init: RequestInit | undefined, name: string) {
  return new Headers(init?.headers).get(name);
}
