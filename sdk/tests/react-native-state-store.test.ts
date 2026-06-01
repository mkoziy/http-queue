import { describe, expect, it } from "bun:test";

import {
  createPersistedTargetStateSecureStore,
  createSecureStateStore,
} from "../src/react-native/secure-state-store";
import { PERSISTED_TARGET_STATE_VERSION, PersistedStateError } from "../src/worker/state";

describe("React Native secure state store", () => {
  it("stores serialized state in secure storage and loads it back", async () => {
    const storage = createStorageAdapter();
    const store = createPersistedTargetStateSecureStore(storage);

    await store.save("target-1", {
      version: PERSISTED_TARGET_STATE_VERSION,
      credentials: {
        workerId: "worker-1",
        token: "token-1",
      },
      nextPollByQueue: {
        orders: 5000,
      },
      currentLease: {
        jobId: "job-1",
        queue: "orders",
        claimedAt: "2026-06-01T12:00:00.000Z",
      },
    });

    expect(storage.values.get("target-1")).toBe(
      JSON.stringify({
        version: PERSISTED_TARGET_STATE_VERSION,
        credentials: {
          workerId: "worker-1",
          token: "token-1",
        },
        nextPollByQueue: {
          orders: 5000,
        },
        currentLease: {
          jobId: "job-1",
          queue: "orders",
          claimedAt: "2026-06-01T12:00:00.000Z",
        },
      }),
    );

    await expect(store.load("target-1")).resolves.toEqual({
      version: PERSISTED_TARGET_STATE_VERSION,
      credentials: {
        workerId: "worker-1",
        token: "token-1",
      },
      nextPollByQueue: {
        orders: 5000,
      },
      currentLease: {
        jobId: "job-1",
        queue: "orders",
        claimedAt: "2026-06-01T12:00:00.000Z",
      },
    });
  });

  it("returns null for missing secure-storage state", async () => {
    const storage = createStorageAdapter();
    const store = createPersistedTargetStateSecureStore(storage);

    await expect(store.load("missing")).resolves.toBeNull();
  });

  it("fails loudly on corrupted secure-storage state", async () => {
    const storage = createStorageAdapter({
      broken: "{",
    });
    const store = createPersistedTargetStateSecureStore(storage);

    await expect(store.load("broken")).rejects.toBeInstanceOf(PersistedStateError);
  });

  it("treats version mismatches as a cache miss and clears them", async () => {
    const storage = createStorageAdapter({
      stale: JSON.stringify({
        version: 2,
        credentials: null,
        nextPollByQueue: {},
        currentLease: null,
      }),
    });
    const store = createPersistedTargetStateSecureStore(storage);

    await expect(store.load("stale")).resolves.toBeNull();
    expect(storage.values.has("stale")).toBe(false);
  });

  it("supports custom serialization contracts", async () => {
    const storage = createStorageAdapter();
    const store = createSecureStateStore<number>({
      storage,
      serialize: (value) => String(value),
      parse: (serialized) => Number(serialized),
    });

    await store.save("count", 42);
    await expect(store.load("count")).resolves.toBe(42);

    await store.clear("count");
    await expect(store.load("count")).resolves.toBeNull();
  });
});

function createStorageAdapter(initialValues?: Record<string, string>) {
  const values = new Map<string, string>(Object.entries(initialValues ?? {}));

  return {
    values,
    getItem(key: string) {
      return Promise.resolve(values.get(key) ?? null);
    },
    setItem(key: string, value: string) {
      values.set(key, value);
      return Promise.resolve();
    },
    removeItem(key: string) {
      values.delete(key);
      return Promise.resolve();
    },
  };
}
