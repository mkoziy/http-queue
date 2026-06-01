import { DEFAULT_RETRY_POLICY, shouldRetryError, type RetryPolicy } from "../core/backoff.js";
import { HttpQueueSdkError } from "../core/errors.js";
import { createLogger, type Logger } from "../core/logger.js";
import { createMemoryStateStore } from "../core/memory-state-store.js";
import type {
  CredentialRefreshReason,
  CredentialSet,
  PersistedTargetState,
  TargetConfig,
} from "../core/types.js";
import type { StateStore } from "../core/state-store.js";
import { WorkerClient, type ClaimedJobResponse } from "./client.js";
import { RoundRobinQueueScheduler } from "./scheduler.js";
import { createEmptyPersistedTargetState } from "./state.js";

export type WorkerRunnerStatus = "stopped" | "running" | "paused";

export interface WorkerJobContext {
  targetKey: string;
  queue: string;
  credentials: CredentialSet;
}

export type WorkerJobHandler = (
  job: ClaimedJobResponse,
  context: WorkerJobContext,
) => Promise<void>;

export interface WorkerRunnerOptions {
  target: TargetConfig;
  handleJob: WorkerJobHandler;
  stateStore?: StateStore<PersistedTargetState>;
  retryPolicy?: RetryPolicy;
  now?: () => number;
  sleep?: (delayMs: number) => Promise<void>;
  logger?: Logger;
}

export class WorkerRunner {
  private readonly target: TargetConfig;
  private readonly handleJob: WorkerJobHandler;
  private readonly stateStore: StateStore<PersistedTargetState>;
  private readonly retryPolicy: RetryPolicy;
  private readonly now: () => number;
  private readonly sleepImplementation: (delayMs: number) => Promise<void>;
  private readonly logger: Logger;
  private readonly stateKey: string;
  private readonly scheduler: RoundRobinQueueScheduler;

  private state = createEmptyPersistedTargetState();
  private loopPromise: Promise<void> | null = null;
  private stopRequested = false;
  private paused = false;
  private wakePromise: Promise<void> | null = null;
  private wakeResolver: (() => void) | null = null;
  private status: WorkerRunnerStatus = "stopped";

  constructor(options: WorkerRunnerOptions) {
    if (options.target.queues.length === 0) {
      throw new Error("WorkerRunner requires at least one queue per target");
    }

    this.target = options.target;
    this.handleJob = options.handleJob;
    this.stateStore = options.stateStore ?? createMemoryStateStore<PersistedTargetState>();
    this.retryPolicy = options.retryPolicy ?? DEFAULT_RETRY_POLICY;
    this.now = options.now ?? Date.now;
    this.sleepImplementation = options.sleep ?? defaultSleep;
    this.logger = options.logger ?? options.target.logger ?? createLogger();
    this.stateKey = options.target.stateKey ?? options.target.key;
    this.scheduler = new RoundRobinQueueScheduler(options.target.queues);
  }

  getStatus(): WorkerRunnerStatus {
    return this.status;
  }

  async start(): Promise<void> {
    if (this.loopPromise) {
      return;
    }

    this.stopRequested = false;
    this.paused = false;
    this.status = "running";
    this.state = (await this.stateStore.load(this.stateKey)) ?? createEmptyPersistedTargetState();
    this.loopPromise = this.runLoop();
  }

  async stop(): Promise<void> {
    this.stopRequested = true;
    this.paused = false;
    this.status = "stopped";
    this.signalWake();

    const loopPromise = this.loopPromise;
    if (loopPromise) {
      await loopPromise;
    }

    this.loopPromise = null;
  }

  pause(): void {
    if (!this.loopPromise || this.paused || this.stopRequested) {
      return;
    }

    this.paused = true;
    this.status = "paused";
    this.signalWake();
  }

  resume(): void {
    if (!this.loopPromise || !this.paused || this.stopRequested) {
      return;
    }

    this.paused = false;
    this.status = "running";
    this.signalWake();
  }

  private async runLoop(): Promise<void> {
    let attempt = 0;

    while (!this.stopRequested) {
      try {
        if (this.paused) {
          await this.waitUntilResumedOrStopped();
          attempt = 0;
          continue;
        }

        const selection = this.scheduler.selectNext(this.now(), this.state.nextPollByQueue);

        if (selection.queue === null) {
          await this.waitForDelay(selection.waitMs);
          attempt = 0;
          continue;
        }

        if (this.stopRequested || this.paused) {
          continue;
        }

        await this.processNextClaim(selection.queue);
        attempt = 0;
      } catch (error) {
        if (this.stopRequested) {
          break;
        }

        if (error instanceof HttpQueueSdkError) {
          const decision = shouldRetryError(error, attempt, this.retryPolicy, 0.5);
          attempt += 1;

          this.logger.warn("worker runner iteration failed", {
            targetKey: this.target.key,
            errorCode: error.code,
            retry: decision.retry,
            delayMs: decision.delayMs,
          });

          if (decision.retry) {
            await this.waitForDelay(decision.delayMs);
            continue;
          }
        } else {
          this.logger.error("worker runner iteration failed with unexpected error", {
            targetKey: this.target.key,
          });
        }

        await this.waitForDelay(this.retryPolicy.baseDelayMs);
      }
    }
  }

  private async processNextClaim(queue: string) {
    const claimResult = await this.executeWithRefresh(
      () => this.createClient().claimNext(queue),
      "claimNext",
      queue,
    );

    this.state.nextPollByQueue[queue] = this.now() + claimResult.nextPollSeconds * 1_000;
    await this.saveState();

    const job = claimResult.job;

    if (!job) {
      return;
    }

    this.state.currentLease = {
      jobId: job.id,
      queue: job.queue,
      claimedAt: new Date(this.now()).toISOString(),
    };
    await this.saveState();

    try {
      await this.handleJob(job, {
        targetKey: this.target.key,
        queue: job.queue,
        credentials: this.requireCredentials(),
      });
    } catch (error) {
      try {
        await this.executeWithRefresh(
          () => this.createClient().nack(job.id),
          "nack",
          queue,
        );
      } finally {
        await this.clearCurrentLease();
      }

      throw error;
    }

    await this.executeWithRefresh(
      () => this.createClient().ack(job.id),
      "ack",
      queue,
    );
    await this.clearCurrentLease();
  }

  private async executeWithRefresh<T>(
    operation: () => Promise<T>,
    operationName: string,
    queue: string,
  ): Promise<T> {
    await this.ensureCredentials("missing");

    try {
      return await operation();
    } catch (error) {
      if (!isAuthError(error)) {
        throw error;
      }

      this.logger.info("worker credentials unauthorized, refreshing", {
        targetKey: this.target.key,
        queue,
        operation: operationName,
      });

      await this.ensureCredentials("unauthorized", true);
      return operation();
    }
  }

  private async ensureCredentials(
    reason: CredentialRefreshReason,
    forceRefresh = false,
  ): Promise<CredentialSet> {
    if (!forceRefresh && this.state.credentials) {
      return this.state.credentials;
    }

    const credentials = await this.target.credentialProvider.getCredentials({
      targetKey: this.target.key,
      reason,
    });

    this.state.credentials = credentials;
    await this.saveState();
    return credentials;
  }

  private createClient() {
    return new WorkerClient({
      baseUrl: this.target.baseUrl,
      token: this.requireCredentials().token,
      fetch: this.target.fetch,
    });
  }

  private requireCredentials() {
    if (!this.state.credentials) {
      throw new Error("WorkerRunner credentials are not loaded");
    }

    return this.state.credentials;
  }

  private async clearCurrentLease() {
    this.state.currentLease = null;
    await this.saveState();
  }

  private async saveState() {
    await this.stateStore.save(this.stateKey, this.state);
  }

  private async waitUntilResumedOrStopped() {
    while (this.paused && !this.stopRequested) {
      await this.waitForWake();
    }
  }

  private async waitForDelay(delayMs: number) {
    if (delayMs <= 0) {
      return;
    }

    await Promise.race([
      this.sleepImplementation(delayMs),
      this.waitForWake(),
    ]);
  }

  private waitForWake() {
    if (!this.wakePromise) {
      this.wakePromise = new Promise<void>((resolve) => {
        this.wakeResolver = () => {
          this.wakePromise = null;
          this.wakeResolver = null;
          resolve();
        };
      });
    }

    return this.wakePromise;
  }

  private signalWake() {
    this.wakeResolver?.();
  }
}

function defaultSleep(delayMs: number) {
  return new Promise<void>((resolve) => {
    setTimeout(resolve, delayMs);
  });
}

function isAuthError(error: unknown): error is HttpQueueSdkError {
  return error instanceof HttpQueueSdkError && error.code === "AUTH_ERROR";
}
