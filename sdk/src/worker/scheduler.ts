export interface QueueSelection {
  queue: string | null;
  waitMs: number;
}

export class RoundRobinQueueScheduler {
  private readonly queues: string[];
  private lastSelectedIndex = -1;

  constructor(queues: string[]) {
    if (queues.length === 0) {
      throw new Error("RoundRobinQueueScheduler requires at least one queue");
    }

    this.queues = [...queues];
  }

  selectNext(now: number, nextPollByQueue: Record<string, number>): QueueSelection {
    for (let offset = 1; offset <= this.queues.length; offset += 1) {
      const index = (this.lastSelectedIndex + offset) % this.queues.length;
      const queue = this.queues[index]!;
      const nextPollAt = nextPollByQueue[queue];

      if (nextPollAt === undefined || nextPollAt <= now) {
        this.lastSelectedIndex = index;
        return {
          queue,
          waitMs: 0,
        };
      }
    }

    let earliestNextPollAt = Number.POSITIVE_INFINITY;

    for (const queue of this.queues) {
      const nextPollAt = nextPollByQueue[queue];

      if (typeof nextPollAt === "number" && nextPollAt < earliestNextPollAt) {
        earliestNextPollAt = nextPollAt;
      }
    }

    return {
      queue: null,
      waitMs: Number.isFinite(earliestNextPollAt) ? Math.max(0, earliestNextPollAt - now) : 0,
    };
  }
}
