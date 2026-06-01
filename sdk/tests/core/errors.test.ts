import { describe, expect, it } from "bun:test";

import {
  createAuthError,
  createInvalidResponseError,
  createNetworkError,
  mapHttpError,
} from "../../src/core/errors";

describe("core error mapping", () => {
  it("maps 401 responses to auth errors", () => {
    const error = mapHttpError(401, { method: "GET", path: "/queues/orders/next" });

    expect(error.code).toBe("AUTH_ERROR");
    expect(error.statusCode).toBe(401);
    expect(error.retryable).toBe(false);
    expect(error.message).toContain("Unauthorized GET /queues/orders/next");
  });

  it("marks retryable http failures correctly", () => {
    const error = mapHttpError(503, { method: "POST", path: "/workers" });

    expect(error.code).toBe("HTTP_ERROR");
    expect(error.retryable).toBe(true);
    expect(error.message).toContain("Unexpected HTTP 503");
  });

  it("keeps invalid response errors non-retryable", () => {
    const error = createInvalidResponseError("missing body", {
      method: "GET",
      path: "/queues/orders/next",
    });

    expect(error.code).toBe("INVALID_RESPONSE");
    expect(error.retryable).toBe(false);
    expect(error.message).toContain("missing body");
  });

  it("treats network failures as retryable", () => {
    const cause = new Error("socket closed");
    const error = createNetworkError(cause, { method: "POST", path: "/jobs/id/ack" });

    expect(error.code).toBe("NETWORK_ERROR");
    expect(error.retryable).toBe(true);
    expect(error.cause).toBe(cause);
  });

  it("constructs explicit auth errors", () => {
    const error = createAuthError({ method: "DELETE", path: "/workers/id" });

    expect(error.code).toBe("AUTH_ERROR");
    expect(error.message).toBe("Unauthorized DELETE /workers/id");
  });
});
