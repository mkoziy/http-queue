import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const sdkDir = resolve(import.meta.dirname, "..");
const npmCacheDir = resolve(sdkDir, ".npm-cache");
const packageJsonPath = resolve(sdkDir, "package.json");
const packageJson = JSON.parse(readFileSync(packageJsonPath, "utf8"));

const packResult = spawnSync(
  "npm",
  ["pack", "--json", "--dry-run", "--ignore-scripts"],
  {
    cwd: sdkDir,
    encoding: "utf8",
    env: {
      ...process.env,
      npm_config_cache: npmCacheDir,
    },
  },
);

if (packResult.status !== 0) {
  process.stderr.write(packResult.stderr);
  process.exit(packResult.status ?? 1);
}

const output = packResult.stdout.trim();
const packSummary = JSON.parse(output)[0];

if (!packSummary || !Array.isArray(packSummary.files)) {
  throw new Error("npm pack did not return file metadata");
}

const packedPaths = new Set(packSummary.files.map((file) => file.path));
const expectedPaths = [
  "package.json",
  "README.md",
  "CHANGELOG.md",
  "dist/index.js",
  "dist/index.d.ts",
  "dist/generated/openapi.js",
  "dist/generated/openapi.d.ts",
];

for (const expectedPath of expectedPaths) {
  if (!packedPaths.has(expectedPath)) {
    throw new Error(`expected published package to include ${expectedPath}`);
  }
}

for (const path of packedPaths) {
  if (path.startsWith("src/") || path.startsWith("tests/") || path.startsWith("examples/")) {
    throw new Error(`expected published package to exclude ${path}`);
  }
}

if (packageJson.version === "0.0.0") {
  throw new Error("package.json version must be set to a real SDK release version");
}

console.log(
  `package smoke check passed for ${packageJson.name}@${packageJson.version} with ${packSummary.files.length} files`,
);
