import { afterEach, describe, expect, it } from "vitest";
import { createAdapter } from "./index.js";
import { HostServiceAddressError } from "@kb-labs/core-platform";
import { manifest } from "./manifest.js";

describe("state broker host address", () => {
  const previous = process.env.KB_STATE_DAEMON_URL;

  afterEach(() => {
    if (previous === undefined) { delete process.env.KB_STATE_DAEMON_URL; }
    else { process.env.KB_STATE_DAEMON_URL = previous; }
  });

  it("returns a typed actionable error when used without a host address", async () => {
    delete process.env.KB_STATE_DAEMON_URL;
    const adapter = createAdapter();
    try {
      await adapter.get("key");
      throw new Error("expected the host address to be required");
    } catch (error) {
      expect(error).toBeInstanceOf(HostServiceAddressError);
      expect(error).toMatchObject({
        code: "KB_HOST_SERVICE_ADDRESS_MISSING",
        hint: expect.stringContaining("kb-create start"),
      });
    }
  });

  it("does not expose host service addresses as adapter config", () => {
    expect(manifest.configSchema).not.toHaveProperty("url");
  });
});
