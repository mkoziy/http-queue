import { execFileSync } from "node:child_process";
import {
  mkdtempSync,
  mkdirSync,
  readFileSync,
  rmSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { dirname, resolve } from "node:path";

const sdkRoot = resolve(import.meta.dirname, "..");
const repoRoot = resolve(sdkRoot, "..");
const inputPath = resolve(repoRoot, "openapi.yaml");
const outputPath = resolve(sdkRoot, "src", "generated", "openapi.ts");
const checkMode = process.argv.includes("--check");
const binaryName =
  process.platform === "win32" ? "openapi-typescript.cmd" : "openapi-typescript";
const binaryPath = resolve(sdkRoot, "node_modules", ".bin", binaryName);

function generate(targetPath) {
  mkdirSync(dirname(targetPath), { recursive: true });
  execFileSync(binaryPath, [inputPath, "-o", targetPath], {
    cwd: sdkRoot,
    stdio: "inherit",
  });
}

function assertInSync() {
  const tempDir = mkdtempSync(resolve(tmpdir(), "http-queue-sdk-openapi-"));
  const tempOutputPath = resolve(tempDir, "openapi.ts");

  try {
    generate(tempOutputPath);

    const committed = readFileSync(outputPath, "utf8");
    const generated = readFileSync(tempOutputPath, "utf8");

    if (committed !== generated) {
      console.error(
        "Generated OpenAPI types are out of date. Run `bun run codegen` in sdk/ and commit the result.",
      );
      process.exitCode = 1;
    }
  } finally {
    rmSync(tempDir, { recursive: true, force: true });
  }
}

if (checkMode) {
  assertInSync();
} else {
  generate(outputPath);
}
