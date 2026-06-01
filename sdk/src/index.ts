export const SDK_PACKAGE_NAME = "http-queue-sdk";

export const SDK_SURFACES = {
  adminClient: "ready",
  workerClient: "ready",
  workerRunner: "pending",
  reactNativeStateStore: "pending",
} as const;

export type {
  CredentialProvider,
  CredentialProviderContext,
  CredentialRefreshReason,
  CredentialSet,
  FetchLike,
  PersistedLease,
  PersistedTargetState,
  TargetConfig,
} from "./core/types.js";
export type { StateStore } from "./core/state-store.js";
export type { LogEvent, LogHandler, LogLevel, Logger } from "./core/logger.js";
export type { ErrorContext, HttpQueueSdkErrorCode } from "./core/errors.js";
export type { RetryDecision, RetryPolicy } from "./core/backoff.js";
export type {
  AdminClientOptions,
  RegisterWorkerResponse,
  ScheduleJobRequest,
  ScheduleJobResponse,
} from "./admin/client.js";
export type {
  ClaimedJobResponse,
  WorkerClaimResult,
  WorkerClientOptions,
} from "./worker/client.js";
export {
  AdminClient,
} from "./admin/client.js";
export {
  WorkerClient,
} from "./worker/client.js";
export {
  createLogger,
} from "./core/logger.js";
export {
  HttpQueueSdkError,
  createAuthError,
  createHttpError,
  createInvalidResponseError,
  createNetworkError,
  mapHttpError,
} from "./core/errors.js";
export {
  assertOk,
  createJsonRequestInit,
  joinUrl,
  normalizeFetchError,
  parseJsonResponse,
  readText,
} from "./core/http.js";
export {
  calculateBackoffDelay,
  DEFAULT_RETRY_POLICY,
  shouldRetryError,
} from "./core/backoff.js";

export interface WorkerRunner {}
