import type { Logger } from "./logger.js";

export interface CredentialSet {
  workerId: string;
  token: string;
}

export type CredentialRefreshReason = "missing" | "unauthorized";

export interface CredentialProviderContext {
  targetKey: string;
  reason: CredentialRefreshReason;
}

export interface CredentialProvider {
  getCredentials(context: CredentialProviderContext): Promise<CredentialSet>;
}

export type FetchLike = (
  input: RequestInfo | URL,
  init?: RequestInit,
) => Promise<Response>;

export interface PersistedLease {
  jobId: string;
  queue: string;
  claimedAt: string;
}

export interface PersistedTargetState {
  version: number;
  credentials: CredentialSet | null;
  nextPollByQueue: Record<string, number>;
  currentLease: PersistedLease | null;
}

export interface TargetConfig {
  key: string;
  baseUrl: string;
  queues: string[];
  credentialProvider: CredentialProvider;
  stateKey?: string;
  fetch?: FetchLike;
  logger?: Logger;
}
