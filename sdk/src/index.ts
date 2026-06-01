export const SDK_PACKAGE_NAME = "http-queue-sdk";

export const SDK_SURFACES = {
  adminClient: "pending",
  workerClient: "pending",
  workerRunner: "pending",
  reactNativeStateStore: "pending",
} as const;

export interface CredentialSet {
  workerId: string;
  token: string;
}

export interface CredentialProviderContext {
  targetKey: string;
}

export interface CredentialProvider {
  getCredentials(context: CredentialProviderContext): Promise<CredentialSet>;
}

export interface StateStore<TState> {
  load(key: string): Promise<TState | null>;
  save(key: string, value: TState): Promise<void>;
  clear(key: string): Promise<void>;
}

export interface Logger {
  debug?(message: string, fields?: Record<string, unknown>): void;
  info?(message: string, fields?: Record<string, unknown>): void;
  warn?(message: string, fields?: Record<string, unknown>): void;
  error?(message: string, fields?: Record<string, unknown>): void;
}

export interface AdminClient {}

export interface WorkerClient {}

export interface WorkerRunner {}
