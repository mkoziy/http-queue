import { describe, expect, it } from "bun:test";

import {
  calculateBackoffDelay,
  DEFAULT_RETRY_POLICY,
  shouldRetryError,
} from "../../src/core/backoff";
import { createInvalidResponseError, createNetworkError, mapHttpError } from "../../src/core/errors";

describe("core backoff policy", () => {
  it("applies exponential delay growth with deterministic jitter", () => {
    const delay = calculateBackoffDelay(3, DEFAULT_RETRY_POLICY, 0.5);

    expect(delay).toBe(1_000);
  });

  it("caps the delay at the configured maximum", () => {
    const delay = calculateBackoffDelay(
      8,
      {
        baseDelayMs: 500,
        maxDelayMs: 2_000,
        maxAttempts: 5,
        jitterRatio: 0,
      },
      0.5,
    );

    expect(delay).toBe(2_000);
  });

  it("retries retryable errors before the max attempt budget is exhausted", () => {
    const decision = shouldRetryError(
      createNetworkError(new Error("reset"), { method: "GET", path: "/queues/orders/next" }),
      1,
      DEFAULT_RETRY_POLICY,
      0.5,
    );

    expect(decision).toEqual({ retry: true, delayMs: 500 });
  });

  it("does not retry non-retryable errors", () => {
    const decision = shouldRetryError(
      createInvalidResponseError("bad json", { method: "GET", path: "/queues/orders/next" }),
      1,
      DEFAULT_RETRY_POLICY,
      0.5,
    );

    expect(decision).toEqual({ retry: false, delayMs: 0 });
  });

  it("does not retry once the attempt budget is exhausted", () => {
    const decision = shouldRetryError(
      mapHttpError(503, { method: "POST", path: "/workers" }),
      DEFAULT_RETRY_POLICY.maxAttempts,
      DEFAULT_RETRY_POLICY,
      0.5,
    );

    expect(decision).toEqual({ retry: false, delayMs: 0 });
  });
});
