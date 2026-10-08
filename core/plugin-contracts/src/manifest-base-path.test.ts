import { describe, expect, it } from "vitest";
import { declaredManifestPath, resolvePluginBasePath, type PluginRouteSurface } from "./manifest.js";

describe("declaredManifestPath", () => {
  it("reads only the canonical kb.manifest field", () => {
    expect(declaredManifestPath({ kb: { manifest: "./dist/manifest.js" } })).toBe("./dist/manifest.js");
    expect(declaredManifestPath({ kbLabs: { manifest: "./dist/legacy.js" } })).toBeUndefined();
    expect(declaredManifestPath({})).toBeUndefined();
  });
});

describe("resolvePluginBasePath", () => {
  const configBasePath = "/api/v1";
  const manifest = (basePath: string | undefined, surface: PluginRouteSurface = "rest") => ({
    id: "@kb-labs/commit",
    ...(surface === "rest" ? { rest: basePath !== undefined ? { basePath } : undefined } : {}),
    ...(surface === "sse" ? { sse: basePath !== undefined ? { basePath } : undefined } : {}),
    ...(surface === "ws" ? { ws: basePath !== undefined ? { basePath } : undefined } : {}),
  });

  it("keeps an /api path as declared", () => {
    expect(resolvePluginBasePath(manifest("/api/custom/plugins/commit"), "rest", configBasePath))
      .toBe("/api/custom/plugins/commit");
  });

  it("rebases a /v1 path onto the configured API base", () => {
    expect(resolvePluginBasePath(manifest("/v1/plugins/commit"), "rest", configBasePath))
      .toBe("/api/v1/plugins/commit");
  });

  it("prefixes a relative manifest path with the configured API base", () => {
    expect(resolvePluginBasePath(manifest("/custom/plugins/commit"), "rest", configBasePath))
      .toBe("/api/v1/custom/plugins/commit");
  });

  it("defaults to the configured plugin namespace", () => {
    expect(resolvePluginBasePath(manifest(undefined), "rest", configBasePath))
      .toBe("/api/v1/plugins/@kb-labs/commit");
    expect(resolvePluginBasePath(manifest(""), "rest", configBasePath))
      .toBe("/api/v1/plugins/@kb-labs/commit");
  });

  it("resolves REST, inherited SSE, and WebSocket bases through the same rules", () => {
    const plugin = {
      id: "@kb-labs/commit",
      rest: { basePath: "/v1/plugins/commit" },
      ws: { basePath: "/v1/ws/plugins/commit" },
    };
    expect(resolvePluginBasePath(plugin, "rest", configBasePath)).toBe("/api/v1/plugins/commit");
    expect(resolvePluginBasePath(plugin, "sse", configBasePath)).toBe("/api/v1/plugins/commit");
    expect(resolvePluginBasePath(plugin, "ws", configBasePath)).toBe("/api/v1/ws/plugins/commit");
  });
});
