import { describe, expect, it } from "vitest";
import { toErrorEnvelope } from "@kb-labs/core-platform";
import { collectProjectServiceDeclarations } from "./project-service-manifest.js";
import type { ServiceManifest } from "./manifest.js";

function manifest(id: string, serviceId: string, rewritePrefix = "/api/v1"): ServiceManifest {
  return {
    schema: "kb.service/1",
    id,
    name: id,
    version: "1.0.0",
    runtime: { entry: "dist/bin.js", healthCheck: "/health" },
    projectApi: { serviceId, rewritePrefix },
  };
}

describe("collectProjectServiceDeclarations", () => {
  it("derives project routing from service manifests", () => {
    expect(collectProjectServiceDeclarations([
      manifest("rest", "rest"),
      manifest("workflow", "workflow"),
      manifest("mcp-daemon", "mcp", "/"),
    ])).toEqual([
      { serviceId: "rest", rewritePrefix: "/api/v1" },
      { serviceId: "workflow", rewritePrefix: "/api/v1" },
      { serviceId: "mcp", rewritePrefix: "/" },
    ]);
  });

  // case: S-221.1
  it("refuses duplicate and invalid public service identifiers with a catalogued config error", () => {
    try {
      collectProjectServiceDeclarations([
        manifest("rest", "rest"),
        manifest("other", "rest"),
      ]);
      throw new Error("expected duplicate project service id to fail");
    } catch (error) {
      expect(toErrorEnvelope(error)).toMatchObject({
        code: "KB_CONFIG_INVALID",
        hint: "Correct projectApi.serviceId and projectApi.rewritePrefix in the service manifest, then restart the host.",
      });
    }
    try {
      collectProjectServiceDeclarations([manifest("rest", "rest/api")]);
      throw new Error("expected invalid project service id to fail");
    } catch (error) {
      expect(toErrorEnvelope(error)).toMatchObject({
        code: "KB_CONFIG_INVALID",
        hint: "Correct projectApi.serviceId and projectApi.rewritePrefix in the service manifest, then restart the host.",
      });
    }
  });
});
