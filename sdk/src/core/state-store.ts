export interface StateStore<TState> {
  load(key: string): Promise<TState | null>;
  save(key: string, value: TState): Promise<void>;
  clear(key: string): Promise<void>;
}
