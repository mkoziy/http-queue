import { HttpQueueSdkError } from "./errors.js";

export interface RetryPolicy {
  baseDelayMs: number;
  maxDelayMs: number;
  maxAttempts: number;
  jitterRatio: number;
}

export interface RetryDecision {
  retry: boolean;
  delayMs: number;
}

export const DEFAULT_RETRY_POLICY: RetryPolicy = {
  baseDelayMs: 250,
  maxDelayMs: 5_000,
  maxAttempts: 3,
  jitterRatio: 0.2,
};

export function calculateBackoffDelay(
  attempt: number,
  policy: RetryPolicy = DEFAULT_RETRY_POLICY,
  random = Math.random(),
) {
  const exponent = Math.max(0, attempt - 1);
  const baseDelay = Math.min(policy.maxDelayMs, policy.baseDelayMs * 2 ** exponent);
  const jitterWindow = baseDelay * policy.jitterRatio;
  const jitterOffset = (random * 2 - 1) * jitterWindow;

  return Math.max(0, Math.round(baseDelay + jitterOffset));
}

export function shouldRetryError(
  error: unknown,
  attempt: number,
  policy: RetryPolicy = DEFAULT_RETRY_POLICY,
  random = Math.random(),
): RetryDecision {
  if (attempt >= policy.maxAttempts) {
    return { retry: false, delayMs: 0 };
  }

  if (!(error instanceof HttpQueueSdkError) || !error.retryable) {
    return { retry: false, delayMs: 0 };
  }

  return {
    retry: true,
    delayMs: calculateBackoffDelay(attempt + 1, policy, random),
  };
}
