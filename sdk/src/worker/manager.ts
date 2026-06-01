import type { RetryPolicy } from "../core/backoff.js";
import type { Logger } from "../core/logger.js";
import type { StateStore } from "../core/state-store.js";
import type { PersistedTargetState, TargetConfig } from "../core/types.js";
import { WorkerRunner, type WorkerJobHandler, type WorkerRunnerOptions, type WorkerRunnerStatus } from "./runner.js";

export interface WorkerManagerOptions {
  targets: TargetConfig[];
  handleJob: WorkerJobHandler;
  stateStore?: StateStore<PersistedTargetState>;
  retryPolicy?: RetryPolicy;
  now?: () => number;
  sleep?: (delayMs: number) => Promise<void>;
  logger?: Logger;
}

export type WorkerManagerStatus = Record<string, WorkerRunnerStatus>;

export class WorkerManager {
  private readonly runners = new Map<string, WorkerRunner>();

  constructor(options: WorkerManagerOptions) {
    if (options.targets.length === 0) {
      throw new Error("WorkerManager requires at least one target");
    }

    for (const target of options.targets) {
      if (this.runners.has(target.key)) {
        throw new Error(`Duplicate target key: ${target.key}`);
      }

      const runnerOptions: WorkerRunnerOptions = {
        target,
        handleJob: options.handleJob,
        stateStore: options.stateStore,
        retryPolicy: options.retryPolicy,
        now: options.now,
        sleep: options.sleep,
        logger: options.logger,
      };

      this.runners.set(target.key, new WorkerRunner(runnerOptions));
    }
  }

  async start(): Promise<void> {
    await Promise.all(this.getRunners().map((runner) => runner.start()));
  }

  async stop(): Promise<void> {
    await Promise.all(this.getRunners().map((runner) => runner.stop()));
  }

  pause(targetKey?: string): void {
    for (const runner of this.getSelectedRunners(targetKey)) {
      runner.pause();
    }
  }

  resume(targetKey?: string): void {
    for (const runner of this.getSelectedRunners(targetKey)) {
      runner.resume();
    }
  }

  getStatus(targetKey?: string): WorkerRunnerStatus | WorkerManagerStatus {
    if (targetKey) {
      return this.requireRunner(targetKey).getStatus();
    }

    return Object.fromEntries(
      this.getRunners().map((runner, index) => {
        const key = Array.from(this.runners.keys())[index]!;
        return [key, runner.getStatus()];
      }),
    );
  }

  getRunner(targetKey: string): WorkerRunner {
    return this.requireRunner(targetKey);
  }

  private getRunners() {
    return Array.from(this.runners.values());
  }

  private getSelectedRunners(targetKey?: string) {
    if (targetKey) {
      return [this.requireRunner(targetKey)];
    }

    return this.getRunners();
  }

  private requireRunner(targetKey: string) {
    const runner = this.runners.get(targetKey);

    if (!runner) {
      throw new Error(`Unknown target key: ${targetKey}`);
    }

    return runner;
  }
}

export function createWorkerManager(options: WorkerManagerOptions) {
  return new WorkerManager(options);
}
