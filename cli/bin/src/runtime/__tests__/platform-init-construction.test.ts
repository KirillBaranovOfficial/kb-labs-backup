import { afterEach, describe, expect, it, vi } from "vitest";
import type { LoadPlatformConfigResult } from "@kb-labs/core-runtime";

const mocks = vi.hoisted(() => {
  const logger = {
    trace() {}, debug() {}, info() {}, warn() {}, error() {}, fatal() {},
    child() { return logger; },
  };
  const platform = {
    logger,
    isInitialized: false,
    registerLifecycleHooks: vi.fn(),
  };
  return { initPlatform: vi.fn(), platform };
});

vi.mock("@kb-labs/core-runtime", async (importOriginal) => ({
  ...await importOriginal<typeof import("@kb-labs/core-runtime")>(),
  initPlatform: mocks.initPlatform,
  platform: mocks.platform,
}));

import { initializePlatform } from "../platform-init.js";

describe("CLI adapter construction", () => {
  afterEach(() => vi.clearAllMocks());

  function projectConfig(projectRoot: string): LoadPlatformConfigResult {
    return {
      platformConfig: { adapters: { storage: "@kb-labs/adapters-fs" } },
      rawConfig: {},
      rawPlatformConfig: {},
      effectiveConfig: {},
      platformRoot: "/installed/platform",
      projectRoot,
      sameLocation: false,
      sources: { roots: { platform: "env", project: "registry", projectStartDir: projectRoot }, fields: {} },
    };
  }

  it("constructs adapters once using the resolved project root for per-project defaults", async () => {
    const projectRoot = "/workspace/app";
    const config = projectConfig(projectRoot);
    const initializedPlatform = { ...mocks.platform, logger: mocks.platform.logger };
    mocks.initPlatform.mockResolvedValue(initializedPlatform);

    const result = await initializePlatform(config);

    expect(mocks.initPlatform).toHaveBeenCalledTimes(1);
    expect(mocks.initPlatform).toHaveBeenCalledWith(
      config.platformConfig,
      projectRoot,
      expect.any(Function),
      "/installed/platform",
      expect.objectContaining({ workspaceDevelopment: false }),
    );
    expect(result.projectRoot).toBe(projectRoot);
  });

  it("does not construct adapters for global help or version", async () => {
    const config = projectConfig("/installed/platform");

    const result = await initializePlatform(config, { skipAdapterInitialization: true });

    expect(mocks.initPlatform).not.toHaveBeenCalled();
    expect(result.platform).toBe(mocks.platform);
  });
});
