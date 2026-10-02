import { afterEach, describe, expect, it, vi } from "vitest";
import { mkdtemp, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";

const mocks = vi.hoisted(() => ({
  initializePlatform: vi.fn(),
  resolveProjectConfig: vi.fn(),
}));

vi.mock("../platform-init.js", () => ({
  initializePlatform: mocks.initializePlatform,
  resolveProjectConfig: mocks.resolveProjectConfig,
}));

import { executeCli } from "../bootstrap.js";

describe("CLI platform and project initialization ordering", () => {
  let isolatedHome: string | undefined;
  const originalEnv = { ...process.env };

  afterEach(async () => {
    if (isolatedHome) await rm(isolatedHome, { recursive: true, force: true });
    isolatedHome = undefined;
    for (const key of Object.keys(process.env)) {
      if (!(key in originalEnv)) delete process.env[key];
    }
    Object.assign(process.env, originalEnv);
    vi.clearAllMocks();
  });

  async function isolateEnvironment(): Promise<void> {
    isolatedHome = await mkdtemp(path.join(os.tmpdir(), "kb-cli-init-order-"));
    process.env.HOME = isolatedHome;
    process.env.KB_HOME = path.join(isolatedHome, ".kb");
    delete process.env.KB_PLATFORM_ROOT;
    delete process.env.KB_PROJECT_ROOT;
    delete process.env.KB_PROJECT_ID;
    delete process.env.KB_NET_OFFSET;
    process.env.KB_LOG_LEVEL = "silent";
  }

  it("initializes platform adapters once, then resolves project config for a project command", async () => {
    await isolateEnvironment();
    const logger = {
      trace() {}, debug() {}, info() {}, warn() {}, error() {}, fatal() {},
      child() { return logger; },
    };
    const platform = { logger };
    mocks.initializePlatform.mockResolvedValue({
      platform,
      platformConfig: { adapters: {} },
      rawConfig: { gateway: { access: { mode: "local" } } },
      effectiveConfig: {},
      platformRoot: isolatedHome,
      projectRoot: isolatedHome,
    });
    mocks.resolveProjectConfig.mockResolvedValue({
      platformConfig: { adapters: {}, plugins: { test: { enabled: true } } },
      rawConfig: { plugins: { test: { enabled: true } } },
      effectiveConfig: { plugins: { test: { enabled: true } } },
      platformRoot: isolatedHome,
      projectRoot: isolatedHome,
    });

    await executeCli(["config", "show", "--help"], {
      cwd: isolatedHome,
      registerBuiltinCommands: () => {},
    });

    expect(mocks.initializePlatform).toHaveBeenCalledTimes(1);
    expect(mocks.initializePlatform).toHaveBeenCalledWith(
      isolatedHome,
      undefined,
      expect.objectContaining({ projectScope: "platform" }),
    );
    expect(mocks.resolveProjectConfig).toHaveBeenCalledTimes(1);
    expect(mocks.resolveProjectConfig).toHaveBeenCalledWith(isolatedHome, undefined);
  });

  it("does not resolve a project for a platform-scoped command", async () => {
    await isolateEnvironment();
    const logger = {
      trace() {}, debug() {}, info() {}, warn() {}, error() {}, fatal() {},
      child() { return logger; },
    };
    mocks.initializePlatform.mockResolvedValue({
      platform: { logger },
      platformConfig: { adapters: {} },
      rawConfig: { gateway: { access: { mode: "local" } } },
      effectiveConfig: {},
      platformRoot: isolatedHome,
      projectRoot: isolatedHome,
    });

    await executeCli(["hello", "--help"], {
      cwd: isolatedHome,
      registerBuiltinCommands: () => {},
    });

    expect(mocks.initializePlatform).toHaveBeenCalledTimes(1);
    expect(mocks.resolveProjectConfig).not.toHaveBeenCalled();
  });
});
