declare const process: {
  env: Record<string, string | undefined>;
  once(event: string, listener: () => void): void;
};

import {
  createLogger,
  createMemoryStateStore,
  createWorkerManager,
  type ClaimedJobResponse,
  type CredentialProvider,
  type CredentialSet,
  type FetchLike,
  type LogEvent,
  type PersistedTargetState,
  type TargetConfig,
} from "../../src/index.js";

interface ProxyWorkerCredentialResponse extends CredentialSet {}

function requireEnv(name: string) {
  const value = process.env[name];

  if (!value) {
    throw new Error(`Missing required environment variable: ${name}`);
  }

  return value;
}

function createProxyCredentialProvider(
  fetchImplementation: FetchLike,
  proxyBaseUrl: string,
  appSessionToken: string,
): CredentialProvider {
  return {
    async getCredentials(context) {
      const response = await fetchImplementation(`${proxyBaseUrl}/queue-credentials`, {
        method: "POST",
        headers: {
          authorization: `Bearer ${appSessionToken}`,
          "content-type": "application/json",
        },
        body: JSON.stringify({
          targetKey: context.targetKey,
          reason: context.reason,
        }),
      });

      if (!response.ok) {
        throw new Error(`Credential proxy returned ${response.status}`);
      }

      const credentials = await response.json() as ProxyWorkerCredentialResponse;
      return credentials;
    },
  };
}

async function handleJob(job: ClaimedJobResponse, context: { targetKey: string; queue: string }) {
  console.log("processing job", {
    id: job.id,
    queue: context.queue,
    targetKey: context.targetKey,
    attempts: job.attempts,
    payload: job.payload,
  });
}

async function main() {
  const fetchImplementation = globalThis.fetch.bind(globalThis);
  const sessionToken = requireEnv("APP_SESSION_TOKEN");
  const proxyBaseUrl = requireEnv("QUEUE_PROXY_BASE_URL");
  const stateStore = createMemoryStateStore<PersistedTargetState>();

  const logEvents: LogEvent[] = [];
  const logger = createLogger((event) => {
    logEvents.push(event);
    console.log(JSON.stringify(event));
  });

  const targets: TargetConfig[] = [
    {
      key: "primary-us",
      baseUrl: "https://queue-us.example.com",
      queues: ["orders", "returns"],
      credentialProvider: createProxyCredentialProvider(
        fetchImplementation,
        proxyBaseUrl,
        sessionToken,
      ),
      fetch: fetchImplementation,
      logger,
    },
    {
      key: "primary-eu",
      baseUrl: "https://queue-eu.example.com",
      queues: ["orders"],
      credentialProvider: createProxyCredentialProvider(
        fetchImplementation,
        proxyBaseUrl,
        sessionToken,
      ),
      fetch: fetchImplementation,
      logger,
    },
  ];

  const manager = createWorkerManager({
    targets,
    stateStore,
    logger,
    handleJob,
  });

  await manager.start();
  console.log("worker manager started", manager.getStatus());

  const shutdown = async () => {
    logger.info("worker manager stopping", {
      bufferedLogEvents: logEvents.length,
    });
    await manager.stop();
  };

  process.once("SIGINT", () => {
    void shutdown();
  });
  process.once("SIGTERM", () => {
    void shutdown();
  });
}

void main();
