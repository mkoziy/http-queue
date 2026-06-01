import { describe, expect, it } from "bun:test";

import { SDK_PACKAGE_NAME, SDK_SURFACES } from "../dist/index.js";

describe("sdk entrypoint", () => {
  it("exports the scaffold package metadata", () => {
    expect(SDK_PACKAGE_NAME).toBe("http-queue-sdk");
    expect(SDK_SURFACES.adminClient).toBe("pending");
    expect(SDK_SURFACES.workerRunner).toBe("pending");
  });
});
