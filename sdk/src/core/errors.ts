export type HttpQueueSdkErrorCode =
  | "AUTH_ERROR"
  | "HTTP_ERROR"
  | "INVALID_RESPONSE"
  | "NETWORK_ERROR";

export interface ErrorContext {
  method?: string;
  path?: string;
  statusCode?: number;
  responseBody?: string;
  cause?: unknown;
}

function formatContext(method?: string, path?: string) {
  if (method && path) {
    return `${method} ${path}`;
  }

  return path ?? method ?? "request";
}

export class HttpQueueSdkError extends Error {
  readonly code: HttpQueueSdkErrorCode;
  readonly statusCode?: number;
  readonly responseBody?: string;
  readonly cause?: unknown;

  constructor(message: string, code: HttpQueueSdkErrorCode, context: ErrorContext = {}) {
    super(message);
    this.name = "HttpQueueSdkError";
    this.code = code;
    this.statusCode = context.statusCode;
    this.responseBody = context.responseBody;
    this.cause = context.cause;
  }

  get retryable() {
    if (this.code === "NETWORK_ERROR") {
      return true;
    }

    if (this.code !== "HTTP_ERROR" || this.statusCode === undefined) {
      return false;
    }

    return this.statusCode === 408 || this.statusCode === 429 || this.statusCode >= 500;
  }
}

export function createAuthError(context: ErrorContext = {}) {
  const request = formatContext(context.method, context.path);
  return new HttpQueueSdkError(`Unauthorized ${request}`, "AUTH_ERROR", context);
}

export function createHttpError(context: ErrorContext = {}) {
  const request = formatContext(context.method, context.path);
  const statusCode = context.statusCode ?? 0;
  return new HttpQueueSdkError(
    `Unexpected HTTP ${statusCode} for ${request}`,
    "HTTP_ERROR",
    context,
  );
}

export function createInvalidResponseError(message: string, context: ErrorContext = {}) {
  const request = formatContext(context.method, context.path);
  return new HttpQueueSdkError(
    `Invalid response for ${request}: ${message}`,
    "INVALID_RESPONSE",
    context,
  );
}

export function createNetworkError(error: unknown, context: ErrorContext = {}) {
  const request = formatContext(context.method, context.path);
  return new HttpQueueSdkError(`Network request failed for ${request}`, "NETWORK_ERROR", {
    ...context,
    cause: error,
  });
}

export function mapHttpError(statusCode: number, context: ErrorContext = {}) {
  if (statusCode === 401) {
    return createAuthError({ ...context, statusCode });
  }

  return createHttpError({ ...context, statusCode });
}
