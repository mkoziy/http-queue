import {
  WorkerRunner,
  createLogger,
  createPersistedTargetStateSecureStore,
  type CredentialProvider,
  type FetchLike,
  type SecureStorageAdapter,
} from "../../src/index.js";

type AppStateStatus = "active" | "background" | "inactive";

interface AppStateSubscription {
  remove(): void;
}

interface AppStateLike {
  addEventListener(
    event: "change",
    listener: (status: AppStateStatus) => void,
  ): AppStateSubscription;
}

interface QueueCredentialResponse {
  workerId: string;
  token: string;
}

function createProxyCredentialProvider(
  fetchImplementation: FetchLike,
  proxyBaseUrl: string,
  sessionToken: string,
): CredentialProvider {
  return {
    async getCredentials(context) {
      const response = await fetchImplementation(`${proxyBaseUrl}/queue-credentials`, {
        method: "POST",
        headers: {
          authorization: `Bearer ${sessionToken}`,
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

      return await response.json() as QueueCredentialResponse;
    },
  };
}

export function createReactNativeWorkerExample(options: {
  appState: AppStateLike;
  secureStorage: SecureStorageAdapter;
  fetchImplementation: FetchLike;
  proxyBaseUrl: string;
  sessionToken: string;
}) {
  const logger = createLogger((event) => {
    console.log("[queue-worker]", event.level, event.message, event.fields);
  });

  const runner = new WorkerRunner({
    target: {
      key: "mobile-primary",
      baseUrl: "https://queue.example.com",
      queues: ["notifications", "sync"],
      credentialProvider: createProxyCredentialProvider(
        options.fetchImplementation,
        options.proxyBaseUrl,
        options.sessionToken,
      ),
      fetch: options.fetchImplementation,
      logger,
    },
    stateStore: createPersistedTargetStateSecureStore(options.secureStorage),
    logger,
    handleJob: async (job, context) => {
      console.log("handle mobile job", {
        jobId: job.id,
        queue: context.queue,
        payload: job.payload,
      });
    },
  });

  const subscription = options.appState.addEventListener("change", (status) => {
    if (status === "active") {
      runner.resume();
      return;
    }

    runner.pause();
  });

  return {
    async start() {
      await runner.start();
    },
    async stop() {
      subscription.remove();
      await runner.stop();
    },
    runner,
  };
}
