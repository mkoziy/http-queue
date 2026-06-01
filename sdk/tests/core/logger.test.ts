import { describe, expect, it } from "bun:test";

import { createLogger } from "../../src/core/logger";

describe("core logger", () => {
  it("emits structured log events through the callback adapter", () => {
    const events: Array<{ level: string; message: string; fields?: Record<string, unknown> }> = [];
    const logger = createLogger((event) => {
      events.push(event);
    });

    logger.info("claimed job", { queue: "orders", jobId: "job-1" });

    expect(events).toEqual([
      {
        level: "info",
        message: "claimed job",
        fields: { queue: "orders", jobId: "job-1" },
      },
    ]);
  });

  it("never throws if the log handler fails", () => {
    const logger = createLogger(() => {
      throw new Error("sink unavailable");
    });

    expect(() => logger.error("worker failed", { targetKey: "primary" })).not.toThrow();
  });

  it("acts as a no-op logger when no handler is configured", () => {
    const logger = createLogger();

    expect(() => logger.debug("polling")).not.toThrow();
    expect(() => logger.warn("backoff", { delayMs: 250 })).not.toThrow();
  });
});
