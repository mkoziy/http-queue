import type { components } from "../generated/openapi.js";
import { assertOk, createJsonRequestInit, joinUrl, normalizeFetchError, parseJsonResponse } from "../core/http.js";
import { createInvalidResponseError } from "../core/errors.js";
import type { FetchLike } from "../core/types.js";

export type ClaimedJobResponse = components["schemas"]["ClaimedJobResponse"];

export interface WorkerClientOptions {
  baseUrl: string;
  token: string;
  fetch?: FetchLike;
}

export interface WorkerClaimResult {
  job: ClaimedJobResponse | null;
  nextPollSeconds: number;
}

export class WorkerClient {
  readonly baseUrl: string;
  readonly token: string;

  private readonly fetchImplementation: FetchLike;

  constructor(options: WorkerClientOptions) {
    this.baseUrl = options.baseUrl;
    this.token = options.token;
    this.fetchImplementation = options.fetch ?? globalThis.fetch.bind(globalThis);
  }

  async claimNext(queue: string): Promise<WorkerClaimResult> {
    const method = "GET";
    const path = `/queues/${encodeURIComponent(queue)}/next`;

    try {
      const response = await this.fetchImplementation(
        joinUrl(this.baseUrl, path),
        {
          method,
          headers: this.createAuthHeaders(),
        },
      );

      await assertOk(response, { method, path });

      const nextPollSeconds = parseNextPollSeconds(response, { method, path });

      if (response.status === 204) {
        return {
          job: null,
          nextPollSeconds,
        };
      }

      return {
        job: await parseJsonResponse<ClaimedJobResponse>(response, { method, path }),
        nextPollSeconds,
      };
    } catch (error) {
      throw normalizeFetchError(error, { method, path });
    }
  }

  async ack(jobId: string): Promise<void> {
    await this.executeMutation("POST", `/jobs/${encodeURIComponent(jobId)}/ack`);
  }

  async nack(jobId: string): Promise<void> {
    await this.executeMutation("POST", `/jobs/${encodeURIComponent(jobId)}/nack`);
  }

  private async executeMutation(method: string, path: string) {
    try {
      const response = await this.fetchImplementation(
        joinUrl(this.baseUrl, path),
        createJsonRequestInit({
          method,
          headers: this.createAuthHeaders(),
        }),
      );

      await assertOk(response, { method, path });
    } catch (error) {
      throw normalizeFetchError(error, { method, path });
    }
  }

  private createAuthHeaders() {
    return new Headers({
      authorization: `Bearer ${this.token}`,
    });
  }
}

function parseNextPollSeconds(response: Response, context: { method: string; path: string }) {
  const header = response.headers.get("X-Next-Poll-Seconds");

  if (header === null) {
    throw createInvalidResponseError("missing X-Next-Poll-Seconds header", context);
  }

  const value = Number(header);

  if (!Number.isInteger(value) || value < 0) {
    throw createInvalidResponseError(
      `invalid X-Next-Poll-Seconds header: ${header}`,
      context,
    );
  }

  return value;
}
