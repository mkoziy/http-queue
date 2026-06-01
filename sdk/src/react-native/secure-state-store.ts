import type { PersistedTargetState } from "../core/types.js";
import type { StateStore } from "../core/state-store.js";
import {
  PersistedStateVersionMismatchError,
  parsePersistedTargetState,
  serializePersistedTargetState,
} from "../worker/state.js";

export interface SecureStorageAdapter {
  getItem(key: string): Promise<string | null>;
  setItem(key: string, value: string): Promise<void>;
  removeItem(key: string): Promise<void>;
}

export interface SecureStateStoreOptions<TState> {
  storage: SecureStorageAdapter;
  parse: (serialized: string) => TState;
  serialize: (value: TState) => string;
}

export function createSecureStateStore<TState>(
  options: SecureStateStoreOptions<TState>,
): StateStore<TState> {
  return {
    async load(key) {
      const serialized = await options.storage.getItem(key);

      if (serialized === null) {
        return null;
      }

      return options.parse(serialized);
    },
    async save(key, value) {
      await options.storage.setItem(key, options.serialize(value));
    },
    async clear(key) {
      await options.storage.removeItem(key);
    },
  };
}

export function createPersistedTargetStateSecureStore(
  storage: SecureStorageAdapter,
): StateStore<PersistedTargetState> {
  const store = createSecureStateStore<PersistedTargetState>({
    storage,
    parse: parsePersistedTargetState,
    serialize: serializePersistedTargetState,
  });

  return {
    async load(key) {
      try {
        return await store.load(key);
      } catch (error) {
        if (error instanceof PersistedStateVersionMismatchError) {
          await storage.removeItem(key);
          return null;
        }

        throw error;
      }
    },
    save(key, value) {
      return store.save(key, value);
    },
    clear(key) {
      return store.clear(key);
    },
  };
}
