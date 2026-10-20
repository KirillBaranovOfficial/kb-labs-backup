import { afterEach, describe, expect, it, vi } from "vitest";
import { mkdtemp, rm, access } from "node:fs/promises";
import os from "node:os";
import path from "node:path";

const mocks = vi.hoisted(() => ({
  loadPlatformConfiguration: vi.fn(),
  resolveProjectConfig: vi.fn(),
  initializePlatform: vi.fn(),
}));

vi.mock("../platform-init.js", () => ({
  loadPlatformConfiguration: mocks.loadPlatformConfiguration,
  resolveProjectConfig: mocks.resolveProjectConfig,
  initializePlatform: mocks.initializePlatform,
}));

import { executeCli } from "../bootstrap.js";

describe("CLI platform configuration, auth, and adapter ordering", () => {
  let isolatedHome: string | undefined;
  const originalEnv = { ...process.env };
  const logger = {
    trace() {}, debug() {}, info() {}, warn() {}, error() {}, fatal() {},
    child() { return logger; },
  };
  const platform = { logger };

  afterEach(async () => {
    if (isolatedHome) await rm(isolatedHome, { recursive: true, force: true });
    isolatedHome = undefined;
    for (const key of Object.keys(process.env)) {
      if (!(key in originalEnv)) delete process.env[key];
    }
    Object.assign(process.env, originalEnv);
    vi.clearAllMocks();
  });

  async function isolateEnvironment(): Promise<string> {
    isolatedHome = await mkdtemp(path.join(os.tmpdir(), "kb-cli-init-order-"));
    process.env.HOME = isolatedHome;
    process.env.KB_HOME = path.join(isolatedHome, ".kb");
    delete process.env.KB_PLATFORM_ROOT;
    delete process.env.KB_PROJECT_ROOT;
    delete process.env.KB_PROJECT_ID;
    delete process.env.KB_NET_OFFSET;
    process.env.KB_LOG_LEVEL = "silent";
    process.env.CLI_VERSION = "2.120.1";
    return isolatedHome;
  }

  function mockConfig(projectRoot: string, projectScope: 'required' | 'platform' = 'platform', rawConfig = { gateway: { access: { mode: "local" } } }) {
    return {
      platformConfig: { adapters: { storage: "@kb-labs/adapters-fs" } },
      rawConfig,
      rawPlatformConfig: rawConfig,
      effectiveConfig: {},
      platformRoot: isolatedHome,
      projectRoot,
      projectScope,
      sameLocation: false,
      sources: { platform: "env", project: "registry", fields: {} },
    };
  }

  function mockInitialization(): void {
    mocks.initializePlatform.mockImplementation(async (config) => ({ ...config, platform }));
  }

  it("loads project config after the gate, then initializes adapters once with the project root", async () => {
    const cwd = await isolateEnvironment();
    const projectRoot = path.join(cwd, "registered-project");
    const platformConfig = mockConfig(cwd);
    const projectConfig = mockConfig(projectRoot, 'required');
    mocks.loadPlatformConfiguration.mockResolvedValue(platformConfig);
    mocks.resolveProjectConfig.mockResolvedValue(projectConfig);
    mockInitialization();

    await executeCli(["config", "show", "--help"], {
      cwd,
      registerBuiltinCommands: () => {},
    });

    expect(mocks.loadPlatformConfiguration).toHaveBeenCalledExactlyOnceWith(cwd, undefined, "platform");
    expect(mocks.resolveProjectConfig).toHaveBeenCalledExactlyOnceWith(cwd, undefined);
    expect(mocks.initializePlatform).toHaveBeenCalledExactlyOnceWith(projectConfig, { skipAdapterInitialization: false });
    expect(mocks.loadPlatformConfiguration.mock.invocationCallOrder[0]).toBeLessThan(
      mocks.resolveProjectConfig.mock.invocationCallOrder[0]!,
    );
    expect(mocks.resolveProjectConfig.mock.invocationCallOrder[0]).toBeLessThan(
      mocks.initializePlatform.mock.invocationCallOrder[0]!,
    );
    // initPlatform receives this projectRoot so built-in defaults resolve to
    // <KB_HOME>/state/<projectId>/ rather than platform-root state.
    expect(mocks.initializePlatform.mock.calls[0]![0].projectRoot).toBe(projectRoot);
  });

  // case: S-048.7
  it('uses explicit platform scope before project initialization for config commands', async () => {
    const cwd = await isolateEnvironment();
    const platformConfig = mockConfig(cwd);
    mocks.loadPlatformConfiguration.mockResolvedValue(platformConfig);
    mockInitialization();

    await executeCli(['config', 'get', '--scope', 'platform', '--help'], { cwd, registerBuiltinCommands: () => {} });

    expect(mocks.resolveProjectConfig).not.toHaveBeenCalled();
    expect(mocks.initializePlatform).toHaveBeenCalledExactlyOnceWith(platformConfig, { skipAdapterInitialization: false });
  });

  it("uses the root-resolution project for plugin discovery while initializing adapters from platform config", async () => {
    const cwd = await isolateEnvironment();
    const projectRoot = path.join(cwd, "registered-project");
    const platformConfig = mockConfig(projectRoot);
    const registerBuiltinCommands = vi.fn();
    mocks.loadPlatformConfiguration.mockResolvedValue(platformConfig);
    mockInitialization();

    await executeCli(["hello", "--help"], {
      cwd,
      registerBuiltinCommands,
    });

    expect(mocks.initializePlatform).toHaveBeenCalledExactlyOnceWith(platformConfig, { skipAdapterInitialization: false });
    expect(mocks.resolveProjectConfig).not.toHaveBeenCalled();
    expect(registerBuiltinCommands).toHaveBeenCalledWith(expect.objectContaining({ projectRoot }));
  });

  // case: S-043.6
  it("uses a registered project root for command discovery before an unknown plugin path is dispatched", async () => {
    const cwd = await isolateEnvironment();
    const projectRoot = path.join(cwd, "registered-project");
    const platformConfig = mockConfig(projectRoot);
    const registerBuiltinCommands = vi.fn();
    mocks.loadPlatformConfiguration.mockResolvedValue(platformConfig);
    mockInitialization();

    await executeCli(["commit", "open"], { cwd, registerBuiltinCommands });

    expect(mocks.resolveProjectConfig).not.toHaveBeenCalled();
    expect(registerBuiltinCommands).toHaveBeenCalledWith(expect.objectContaining({
      platformRoot: cwd,
      projectRoot,
    }));
  });

  it("skips adapter construction for global help and version without creating .kb state", async () => {
    const cwd = await isolateEnvironment();
    const platformConfig = mockConfig(cwd);
    mocks.loadPlatformConfiguration.mockResolvedValue(platformConfig);
    mockInitialization();

    for (const args of [["--help"], ["--version"]]) {
      await executeCli(args, { cwd, registerBuiltinCommands: () => {} });
    }

    expect(mocks.initializePlatform).toHaveBeenCalledTimes(2);
    expect(mocks.initializePlatform).toHaveBeenNthCalledWith(1, platformConfig, { skipAdapterInitialization: true });
    expect(mocks.initializePlatform).toHaveBeenNthCalledWith(2, platformConfig, { skipAdapterInitialization: true });
    expect(mocks.resolveProjectConfig).not.toHaveBeenCalled();
    await expect(access(path.join(cwd, ".kb"))).rejects.toThrow();
    await expect(access(process.env.KB_HOME!)).rejects.toThrow();
  });
});
