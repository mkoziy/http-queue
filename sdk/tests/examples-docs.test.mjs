import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";

import { describe, expect, it } from "bun:test";

const sdkDir = resolve(import.meta.dirname, "..");
const readmePath = resolve(sdkDir, "README.md");
const examplePaths = [
  "examples/node-admin/index.ts",
  "examples/node-worker/index.ts",
  "examples/react-native-worker/index.ts",
];

describe("examples and docs", () => {
  it("ships the documented example files", () => {
    for (const relativePath of examplePaths) {
      expect(existsSync(resolve(sdkDir, relativePath))).toBe(true);
    }
  });

  it("documents the runtime behaviors covered by the examples", () => {
    const readme = readFileSync(readmePath, "utf8");

    expect(readme).toContain("examples/node-admin/index.ts");
    expect(readme).toContain("examples/node-worker/index.ts");
    expect(readme).toContain("examples/react-native-worker/index.ts");
    expect(readme).toContain("Logging");
    expect(readme).toContain("Credential refresh");
    expect(readme).toContain("Multi-target workers");
    expect(readme).toContain("Queue scheduling");
  });
});
