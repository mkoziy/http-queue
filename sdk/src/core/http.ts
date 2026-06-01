import {
  createInvalidResponseError,
  createNetworkError,
  mapHttpError,
} from "./errors.js";

export interface JsonRequestOptions {
  method: string;
  headers?: HeadersInit;
  body?: unknown;
}

export function joinUrl(baseUrl: string, path: string) {
  return new URL(path, ensureTrailingSlash(baseUrl)).toString();
}

export function createJsonRequestInit(options: JsonRequestOptions): RequestInit {
  const headers = new Headers(options.headers);

  if (options.body !== undefined) {
    headers.set("content-type", "application/json");
  }

  return {
    method: options.method,
    headers,
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
  };
}

export async function readText(response: Response) {
  return response.text();
}

export async function parseJsonResponse<T>(
  response: Response,
  context: { method: string; path: string },
): Promise<T> {
  const body = await readText(response);

  if (!body) {
    throw createInvalidResponseError("expected JSON body but received empty response", context);
  }

  try {
    return JSON.parse(body) as T;
  } catch (error) {
    throw createInvalidResponseError("response body is not valid JSON", {
      ...context,
      cause: error,
      responseBody: body,
    });
  }
}

export async function assertOk(
  response: Response,
  context: { method: string; path: string },
): Promise<void> {
  if (response.ok) {
    return;
  }

  throw mapHttpError(response.status, {
    ...context,
    responseBody: await readText(response),
  });
}

export function normalizeFetchError(
  error: unknown,
  context: { method: string; path: string },
) {
  if (error instanceof Error && error.name === "HttpQueueSdkError") {
    return error;
  }

  return createNetworkError(error, context);
}

function ensureTrailingSlash(value: string) {
  return value.endsWith("/") ? value : `${value}/`;
}
