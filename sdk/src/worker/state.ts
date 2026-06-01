import type { CredentialSet, PersistedLease, PersistedTargetState } from "../core/types.js";

export const PERSISTED_TARGET_STATE_VERSION = 1;

export class PersistedStateError extends Error {
  constructor(message: string, options?: { cause?: unknown }) {
    super(message, options);
    this.name = "PersistedStateError";
  }
}

export class PersistedStateVersionMismatchError extends PersistedStateError {
  readonly foundVersion: number;
  readonly expectedVersion: number;

  constructor(foundVersion: number, expectedVersion = PERSISTED_TARGET_STATE_VERSION) {
    super(
      `Persisted target state version ${foundVersion} does not match supported version ${expectedVersion}`,
    );
    this.name = "PersistedStateVersionMismatchError";
    this.foundVersion = foundVersion;
    this.expectedVersion = expectedVersion;
  }
}

export function createEmptyPersistedTargetState(): PersistedTargetState {
  return {
    version: PERSISTED_TARGET_STATE_VERSION,
    credentials: null,
    nextPollByQueue: {},
    currentLease: null,
  };
}

export function serializePersistedTargetState(state: PersistedTargetState) {
  assertPersistedTargetState(state);
  return JSON.stringify(state);
}

export function parsePersistedTargetState(serialized: string): PersistedTargetState {
  let parsed: unknown;

  try {
    parsed = JSON.parse(serialized);
  } catch (error) {
    throw new PersistedStateError("Persisted target state is not valid JSON", { cause: error });
  }

  return assertPersistedTargetState(parsed);
}

function assertPersistedTargetState(value: unknown): PersistedTargetState {
  if (!isRecord(value)) {
    throw new PersistedStateError("Persisted target state must be an object");
  }

  const rawVersion = value.version;

  if (!Number.isInteger(rawVersion)) {
    throw new PersistedStateError("Persisted target state version must be an integer");
  }

  const version = rawVersion as number;

  if (version !== PERSISTED_TARGET_STATE_VERSION) {
    throw new PersistedStateVersionMismatchError(version);
  }

  return {
    version,
    credentials: parseCredentials(value.credentials),
    nextPollByQueue: parseNextPollByQueue(value.nextPollByQueue),
    currentLease: parseCurrentLease(value.currentLease),
  };
}

function parseCredentials(value: unknown): CredentialSet | null {
  if (value === null) {
    return null;
  }

  if (!isRecord(value) || typeof value.workerId !== "string" || typeof value.token !== "string") {
    throw new PersistedStateError("Persisted credentials must contain workerId and token strings");
  }

  return {
    workerId: value.workerId,
    token: value.token,
  };
}

function parseNextPollByQueue(value: unknown): Record<string, number> {
  if (!isRecord(value)) {
    throw new PersistedStateError("Persisted nextPollByQueue must be an object");
  }

  const result: Record<string, number> = {};

  for (const [queue, nextPoll] of Object.entries(value)) {
    if (typeof nextPoll !== "number" || !Number.isFinite(nextPoll) || nextPoll < 0) {
      throw new PersistedStateError(
        `Persisted nextPollByQueue entry for ${queue} must be a non-negative number`,
      );
    }

    result[queue] = nextPoll;
  }

  return result;
}

function parseCurrentLease(value: unknown): PersistedLease | null {
  if (value === null) {
    return null;
  }

  if (
    !isRecord(value)
    || typeof value.jobId !== "string"
    || typeof value.queue !== "string"
    || typeof value.claimedAt !== "string"
  ) {
    throw new PersistedStateError(
      "Persisted currentLease must contain jobId, queue, and claimedAt strings",
    );
  }

  return {
    jobId: value.jobId,
    queue: value.queue,
    claimedAt: value.claimedAt,
  };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
