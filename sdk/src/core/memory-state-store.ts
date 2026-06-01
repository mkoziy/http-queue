import type { StateStore } from "./state-store.js";

export class MemoryStateStore<TState> implements StateStore<TState> {
  private readonly values = new Map<string, TState>();

  async load(key: string): Promise<TState | null> {
    const value = this.values.get(key);
    return value === undefined ? null : cloneState(value);
  }

  async save(key: string, value: TState): Promise<void> {
    this.values.set(key, cloneState(value));
  }

  async clear(key: string): Promise<void> {
    this.values.delete(key);
  }
}

export function createMemoryStateStore<TState>(): StateStore<TState> {
  return new MemoryStateStore<TState>();
}

function cloneState<TState>(value: TState): TState {
  return structuredClone(value);
}
