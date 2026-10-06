import { AdapterConfigError } from "@kb-labs/core-platform";
import { describe, expect, it } from "vitest";
import { hostStartupFailure } from "../startup-error.js";

describe("host startup failure result", () => {
  it("preserves the catalogued adapter configuration diagnostic for the launcher", () => {
    const result = hostStartupFailure(new AdapterConfigError(
      "platform.adapterOptions.analytics.retentionDays",
      "must be a positive integer",
      "kb config set --scope platform platform.adapterOptions.analytics.retentionDays 90",
    ));

    expect(result.ok).toBe(false);
    expect(result.error).toMatchObject({
      code: "KB_CONFIG_INVALID",
      retryable: false,
      cause: "must be a positive integer",
      hint: "kb config set --scope platform platform.adapterOptions.analytics.retentionDays 90",
      details: { path: "platform.adapterOptions.analytics.retentionDays" },
    });
  });
});
