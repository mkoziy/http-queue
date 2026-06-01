import { describe, expect, it } from "bun:test";

import { WorkerClient } from "../src/worker/client";
import type { FetchLike } from "../src/core/types";
import { HttpQueueSdkError } from "../src/core/errors";

describe("WorkerClient", () => {
  it("claims jobs, parses next-poll hints, and sends bearer auth", async () => {
    const client = new WorkerClient({
      baseUrl: "https://queue.example",
      token: "worker-token",
      fetch: createFetchStub((input, init) => {
        expect(String(input)).toBe("https://queue.example/queues/orders/next");
        expect(init?.method).toBe("GET");
        expect(readHeader(init, "authorization")).toBe("Bearer worker-token");

        return new Response(
          JSON.stringify({
            id: "job-1",
            queue: "orders",
            payload: { orderId: 42 },
            attempts: 2,
          }),
          {
            status: 200,
            headers: {
              "content-type": "application/json",
              "X-Next-Poll-Seconds": "7",
            },
          },
        );
      }),
    });

    await expect(client.claimNext("orders")).resolves.toEqual({
      job: {
        id: "job-1",
        queue: "orders",
        payload: { orderId: 42 },
        attempts: 2,
      },
      nextPollSeconds: 7,
    });
  });

  it("returns empty claims with the advisory header", async () => {
    const client = new WorkerClient({
      baseUrl: "https://queue.example",
      token: "worker-token",
      fetch: createFetchStub(() =>
        new Response(null, {
          status: 204,
          headers: {
            "X-Next-Poll-Seconds": "3",
          },
        }),
      ),
    });

    await expect(client.claimNext("orders")).resolves.toEqual({
      job: null,
      nextPollSeconds: 3,
    });
  });

  it("supports ack and nack mutations", async () => {
    const requests: string[] = [];
    const client = new WorkerClient({
      baseUrl: "https://queue.example",
      token: "worker-token",
      fetch: createFetchStub((input, init) => {
        requests.push(`${init?.method} ${String(input)}`);
        expect(readHeader(init, "authorization")).toBe("Bearer worker-token");
        return new Response(null, { status: 204 });
      }),
    });

    await client.ack("job-1");
    await client.nack("job-1");

    expect(requests).toEqual([
      "POST https://queue.example/jobs/job-1/ack",
      "POST https://queue.example/jobs/job-1/nack",
    ]);
  });

  it("maps auth failures during worker operations", async () => {
    const client = new WorkerClient({
      baseUrl: "https://queue.example",
      token: "worker-token",
      fetch: createFetchStub(() => new Response("unauthorized", { status: 401 })),
    });

    await expect(client.ack("job-1")).rejects.toMatchObject<HttpQueueSdkError>({
      code: "AUTH_ERROR",
      statusCode: 401,
    });
  });

  it("rejects missing or invalid next-poll headers", async () => {
    const missingHeaderClient = new WorkerClient({
      baseUrl: "https://queue.example",
      token: "worker-token",
      fetch: createFetchStub(
        () =>
          new Response(JSON.stringify({ id: "job-1", queue: "orders", payload: null, attempts: 1 }), {
            status: 200,
            headers: {
              "content-type": "application/json",
            },
          }),
      ),
    });

    await expect(missingHeaderClient.claimNext("orders")).rejects.toMatchObject<HttpQueueSdkError>({
      code: "INVALID_RESPONSE",
    });

    const invalidHeaderClient = new WorkerClient({
      baseUrl: "https://queue.example",
      token: "worker-token",
      fetch: createFetchStub(
        () =>
          new Response(JSON.stringify({ id: "job-1", queue: "orders", payload: null, attempts: 1 }), {
            status: 200,
            headers: {
              "content-type": "application/json",
              "X-Next-Poll-Seconds": "soon",
            },
          }),
      ),
    });

    await expect(invalidHeaderClient.claimNext("orders")).rejects.toMatchObject<HttpQueueSdkError>({
      code: "INVALID_RESPONSE",
    });
  });
});

function createFetchStub(handler: NonNullable<FetchLike>): FetchLike {
  return ((input, init) => handler(input, init)) as FetchLike;
}

function readHeader(init: RequestInit | undefined, name: string) {
  return new Headers(init?.headers).get(name);
}
