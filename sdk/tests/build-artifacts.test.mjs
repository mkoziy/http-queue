import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";

import { describe, expect, it } from "bun:test";

const sdkDir = resolve(import.meta.dirname, "..");
const distDir = resolve(sdkDir, "dist");
const packageJsonPath = resolve(sdkDir, "package.json");

describe("built package artifacts", () => {
  it("emits the compiled entrypoint and declarations", () => {
    expect(existsSync(resolve(distDir, "index.js"))).toBe(true);
    expect(existsSync(resolve(distDir, "index.d.ts"))).toBe(true);
  });

  it("keeps package exports aligned with the dist output", () => {
    const packageJson = JSON.parse(readFileSync(packageJsonPath, "utf8"));

    expect(packageJson.exports["."].import).toBe("./dist/index.js");
    expect(packageJson.exports["."].types).toBe("./dist/index.d.ts");
    expect(packageJson.types).toBe("./dist/index.d.ts");
    expect(packageJson.files).toContain("dist");
  });
});
