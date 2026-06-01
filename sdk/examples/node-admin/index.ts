declare const process: {
  env: Record<string, string | undefined>;
};

import { AdminClient, type ScheduleJobRequest } from "../../src/index.js";

function requireEnv(name: string) {
  const value = process.env[name];

  if (!value) {
    throw new Error(`Missing required environment variable: ${name}`);
  }

  return value;
}

async function main() {
  const client = new AdminClient({
    baseUrl: requireEnv("HTTP_QUEUE_BASE_URL"),
    username: requireEnv("HTTP_QUEUE_ADMIN_USER"),
    password: requireEnv("HTTP_QUEUE_ADMIN_PASS"),
  });

  const job: ScheduleJobRequest = {
    payload: {
      orderId: "order-123",
      action: "ship",
    },
    ttl: 600,
  };

  const scheduled = await client.scheduleJob("orders", job);
  console.log("scheduled job", scheduled.id, "on", scheduled.queue);

  const worker = await client.registerWorker();
  console.log("registered worker", worker.worker_id);

  await client.deregisterWorker(worker.worker_id);
  console.log("deregistered worker", worker.worker_id);
}

void main();
