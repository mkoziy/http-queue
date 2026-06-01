import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";

import { describe, expect, it } from "bun:test";

const sdkDir = resolve(import.meta.dirname, "..");
const generatedTypesPath = resolve(sdkDir, "src", "generated", "openapi.ts");

describe("generated OpenAPI types", () => {
  it("commits the generated schema file", () => {
    expect(existsSync(generatedTypesPath)).toBe(true);
  });

  it("includes the expected core API shapes", () => {
    const contents = readFileSync(generatedTypesPath, "utf8");

    expect(contents).toContain("export interface paths");
    expect(contents).toContain("ScheduleJobResponse");
    expect(contents).toContain("RegisterWorkerResponse");
    expect(contents).toContain("\"X-Next-Poll-Seconds\"");
  });
});
