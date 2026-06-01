import type { components } from "../generated/openapi.js";
import { assertOk, createJsonRequestInit, joinUrl, normalizeFetchError, parseJsonResponse } from "../core/http.js";
import type { FetchLike } from "../core/types.js";

export type ScheduleJobRequest = components["schemas"]["ScheduleJobRequest"];
export type ScheduleJobResponse = components["schemas"]["ScheduleJobResponse"];
export type RegisterWorkerResponse = components["schemas"]["RegisterWorkerResponse"];

export interface AdminClientOptions {
  baseUrl: string;
  username: string;
  password: string;
  fetch?: FetchLike;
}

export class AdminClient {
  readonly baseUrl: string;
  readonly username: string;
  readonly password: string;

  private readonly fetchImplementation: FetchLike;

  constructor(options: AdminClientOptions) {
    this.baseUrl = options.baseUrl;
    this.username = options.username;
    this.password = options.password;
    this.fetchImplementation = options.fetch ?? globalThis.fetch.bind(globalThis);
  }

  async scheduleJob(queue: string, request: ScheduleJobRequest): Promise<ScheduleJobResponse> {
    const path = `/queues/${encodeURIComponent(queue)}/jobs`;
    const response = await this.execute("POST", path, request);
    return parseJsonResponse<ScheduleJobResponse>(response, { method: "POST", path });
  }

  async registerWorker(): Promise<RegisterWorkerResponse> {
    const path = "/workers";
    const response = await this.execute("POST", path);
    return parseJsonResponse<RegisterWorkerResponse>(response, { method: "POST", path });
  }

  async deregisterWorker(workerId: string): Promise<void> {
    const path = `/workers/${encodeURIComponent(workerId)}`;
    await this.execute("DELETE", path);
  }

  private async execute(method: string, path: string, body?: unknown) {
    try {
      const response = await this.fetchImplementation(
        joinUrl(this.baseUrl, path),
        createJsonRequestInit({
          method,
          headers: this.createAuthHeaders(),
          body,
        }),
      );

      await assertOk(response, { method, path });
      return response;
    } catch (error) {
      throw normalizeFetchError(error, { method, path });
    }
  }

  private createAuthHeaders() {
    return new Headers({
      authorization: `Basic ${btoa(`${this.username}:${this.password}`)}`,
    });
  }
}
