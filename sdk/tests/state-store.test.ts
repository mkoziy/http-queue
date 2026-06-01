import { describe, expect, it } from "bun:test";

import { createMemoryStateStore } from "../src/core/memory-state-store";
import {
  PERSISTED_TARGET_STATE_VERSION,
  PersistedStateError,
  PersistedStateVersionMismatchError,
  createEmptyPersistedTargetState,
  parsePersistedTargetState,
  serializePersistedTargetState,
} from "../src/worker/state";

describe("persisted target state codec", () => {
  it("serializes and parses the versioned target state", () => {
    const serialized = serializePersistedTargetState({
      version: PERSISTED_TARGET_STATE_VERSION,
      credentials: {
        workerId: "worker-1",
        token: "token-1",
      },
      nextPollByQueue: {
        orders: 1234,
      },
      currentLease: {
        jobId: "job-1",
        queue: "orders",
        claimedAt: "2026-06-01T12:00:00.000Z",
      },
    });

    expect(parsePersistedTargetState(serialized)).toEqual({
      version: PERSISTED_TARGET_STATE_VERSION,
      credentials: {
        workerId: "worker-1",
        token: "token-1",
      },
      nextPollByQueue: {
        orders: 1234,
      },
      currentLease: {
        jobId: "job-1",
        queue: "orders",
        claimedAt: "2026-06-01T12:00:00.000Z",
      },
    });
  });

  it("creates an empty versioned target state", () => {
    expect(createEmptyPersistedTargetState()).toEqual({
      version: PERSISTED_TARGET_STATE_VERSION,
      credentials: null,
      nextPollByQueue: {},
      currentLease: null,
    });
  });

  it("rejects corrupted persisted state", () => {
    expect(() => parsePersistedTargetState("{")).toThrow(PersistedStateError);
    expect(() =>
      parsePersistedTargetState(
        JSON.stringify({
          version: PERSISTED_TARGET_STATE_VERSION,
          credentials: null,
          nextPollByQueue: "bad",
          currentLease: null,
        }),
      ),
    ).toThrow(PersistedStateError);
  });

  it("rejects unsupported state versions", () => {
    expect(() =>
      parsePersistedTargetState(
        JSON.stringify({
          version: 999,
          credentials: null,
          nextPollByQueue: {},
          currentLease: null,
        }),
      ),
    ).toThrow(PersistedStateVersionMismatchError);
  });
});

describe("MemoryStateStore", () => {
  it("returns null for missing entries", async () => {
    const store = createMemoryStateStore<Record<string, unknown>>();

    await expect(store.load("missing")).resolves.toBeNull();
  });

  it("stores cloned state values and clears them", async () => {
    const store = createMemoryStateStore<{
      nested: { count: number };
    }>();

    const value = {
      nested: { count: 1 },
    };

    await store.save("target", value);
    value.nested.count = 2;

    await expect(store.load("target")).resolves.toEqual({
      nested: { count: 1 },
    });

    const loaded = await store.load("target");
    if (loaded === null) {
      throw new Error("expected saved state");
    }

    loaded.nested.count = 3;

    await expect(store.load("target")).resolves.toEqual({
      nested: { count: 1 },
    });

    await store.clear("target");
    await expect(store.load("target")).resolves.toBeNull();
  });
});
