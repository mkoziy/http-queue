import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const sdkDir = resolve(import.meta.dirname, "..");
const npmCacheDir = resolve(sdkDir, ".npm-cache");
const packageJsonPath = resolve(sdkDir, "package.json");
const packageJson = JSON.parse(readFileSync(packageJsonPath, "utf8"));
const dryRun = process.argv.includes("--dry-run");

run("bun", ["run", "release:check"]);
run("npm", dryRun ? ["publish", "--dry-run", "--ignore-scripts"] : ["publish", "--ignore-scripts"], {
  ...process.env,
  npm_config_cache: npmCacheDir,
});

console.log(
  `${dryRun ? "Dry-run publish verified" : "Published"} ${packageJson.name}@${packageJson.version}`,
);

function run(command, args, env = process.env) {
  const result = spawnSync(command, args, {
    cwd: sdkDir,
    stdio: "inherit",
    env,
  });

  if (result.status !== 0) {
    process.exit(result.status ?? 1);
  }
}
