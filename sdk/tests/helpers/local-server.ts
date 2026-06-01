import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

export interface LocalServerHandle {
  adminPassword: string;
  adminUsername: string;
  baseUrl: string;
  stop(): Promise<void>;
}

export async function startLocalServer(): Promise<LocalServerHandle> {
  const rootDir = resolve(import.meta.dirname, "../../..");
  const tempDir = await mkdtemp(join(tmpdir(), "http-queue-sdk-integration-"));
  const badgerPath = join(tempDir, "badger");
  const binaryPath = join(tempDir, "http-queue-sdk-server");
  const portFile = join(tempDir, "port");
  const adminUsername = "sdk-admin";
  const adminPassword = "sdk-secret";

  const buildResult = Bun.spawnSync({
    cmd: ["go", "build", "-o", binaryPath, "."],
    cwd: rootDir,
    stdout: "pipe",
    stderr: "pipe",
  });

  if (buildResult.exitCode !== 0) {
    throw new Error(
      `Failed to build local server: ${decodeOutput(buildResult.stderr) || decodeOutput(buildResult.stdout)}`,
    );
  }

  const process = Bun.spawn({
    cmd: [binaryPath],
    cwd: rootDir,
    env: {
      ...processEnvRecord(),
      ADMIN_USER: adminUsername,
      ADMIN_PASS: adminPassword,
      PORT: "0",
      PORT_FILE: portFile,
      BADGER_PATH: badgerPath,
      VISIBILITY_TIMEOUT: "2s",
      WORKER_EXPIRY: "5s",
      SWEEP_INTERVAL: "1s",
      MAX_ATTEMPTS: "3",
      LAST_SEEN_DEBOUNCE: "100ms",
      WORKER_NEXT_MIN_INTERVAL: "1s",
      WORKER_NEXT_MAX_INTERVAL: "2s",
    },
    stdout: "pipe",
    stderr: "pipe",
  });

  try {
    const port = await waitForPort(portFile);
    const baseUrl = `http://127.0.0.1:${port}`;
    await waitForReady(baseUrl);

    return {
      adminPassword,
      adminUsername,
      baseUrl,
      async stop() {
        process.kill();
        await process.exited;
        await rm(tempDir, { recursive: true, force: true });
      },
    };
  } catch (error) {
    process.kill();
    await process.exited;
    const stderr = await readStream(process.stderr);
    await rm(tempDir, { recursive: true, force: true });

    if (error instanceof Error && stderr) {
      throw new Error(`${error.message}\nserver stderr:\n${stderr}`.trim());
    }

    throw error;
  }
}

async function waitForPort(portFile: string) {
  for (let attempt = 0; attempt < 150; attempt += 1) {
    try {
      const value = (await readFile(portFile, "utf8")).trim();

      if (value) {
        return value;
      }
    } catch {
      // Wait for the server to create the port file.
    }

    await sleep(100);
  }

  throw new Error("Local server did not report a port in time");
}

async function waitForReady(baseUrl: string) {
  for (let attempt = 0; attempt < 150; attempt += 1) {
    try {
      const response = await fetch(`${baseUrl}/workers`, {
        method: "POST",
      });

      if (response.status === 401) {
        return;
      }
    } catch {
      // Wait for server readiness.
    }

    await sleep(100);
  }

  throw new Error(`Local server at ${baseUrl} did not become ready in time`);
}

function decodeOutput(output: Uint8Array<ArrayBufferLike> | null | undefined) {
  return output ? new TextDecoder().decode(output).trim() : "";
}

async function readStream(stream: ReadableStream<Uint8Array> | null | undefined) {
  if (!stream) {
    return "";
  }

  return new Response(stream).text();
}

function processEnvRecord() {
  return Object.fromEntries(
    Object.entries(process.env).filter((entry): entry is [string, string] => typeof entry[1] === "string"),
  );
}

function sleep(delayMs: number) {
  return new Promise<void>((resolve) => {
    setTimeout(resolve, delayMs);
  });
}
