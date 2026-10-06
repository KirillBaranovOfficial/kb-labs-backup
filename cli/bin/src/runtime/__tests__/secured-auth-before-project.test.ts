import { afterEach, describe, expect, it, vi } from 'vitest';
import { mkdtemp, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';

const mocks = vi.hoisted(() => ({
  loadPlatformConfiguration: vi.fn(),
  resolveProjectConfig: vi.fn(),
  initializePlatform: vi.fn(),
  hasUsablePlatformAuth: vi.fn(),
  tryResolveGateway: vi.fn(),
  executeViaGateway: vi.fn(),
  logger: {
    trace: vi.fn(), debug: vi.fn(), info: vi.fn(), warn: vi.fn(), error: vi.fn(), fatal: vi.fn(),
    child() { return this; },
  },
}));

vi.mock('../platform-init.js', () => ({
  loadPlatformConfiguration: mocks.loadPlatformConfiguration,
  resolveProjectConfig: mocks.resolveProjectConfig,
  initializePlatform: mocks.initializePlatform,
}));
vi.mock('@kb-labs/cli-runtime/gateway', () => ({ hasUsablePlatformAuth: mocks.hasUsablePlatformAuth }));
vi.mock('../gateway-executor.js', () => ({
  tryResolveGateway: mocks.tryResolveGateway,
  executeViaGateway: mocks.executeViaGateway,
}));

import { executeCli } from '../bootstrap.js';

describe('secured CLI auth ordering', () => {
  let isolatedHome: string | undefined;
  const originalHome = process.env.HOME;
  const originalKbHome = process.env.KB_HOME;
  const originalPlatformRoot = process.env.KB_PLATFORM_ROOT;
  const originalNetOffset = process.env.KB_NET_OFFSET;
  const originalLogStream = process.env.KB_LOG_STREAM;
  const originalOutputMode = process.env.KB_OUTPUT_MODE;

  afterEach(async () => {
    if (isolatedHome) await rm(isolatedHome, { recursive: true, force: true });
    isolatedHome = undefined;
    process.env.HOME = originalHome;
    process.env.KB_HOME = originalKbHome;
    process.env.KB_PLATFORM_ROOT = originalPlatformRoot;
    process.env.KB_NET_OFFSET = originalNetOffset;
    process.env.KB_LOG_STREAM = originalLogStream;
    process.env.KB_OUTPUT_MODE = originalOutputMode;
    vi.clearAllMocks();
  });

  // case: S-041.7, S-041.17
  it('returns the auth envelope from an unregistered project directory before project resolution', async () => {
    isolatedHome = await mkdtemp(path.join(os.tmpdir(), 'kb-cli-secured-auth-'));
    process.env.HOME = isolatedHome;
    process.env.KB_HOME = path.join(isolatedHome, '.kb');
    delete process.env.KB_PLATFORM_ROOT;
    delete process.env.KB_NET_OFFSET;

    mocks.loadPlatformConfiguration.mockResolvedValue({
      platformConfig: {},
      rawConfig: { gateway: { access: { mode: 'secured' } } },
      rawPlatformConfig: { gateway: { access: { mode: 'secured' } } },
      effectiveConfig: undefined,
      platformRoot: isolatedHome,
      projectRoot: isolatedHome,
      sameLocation: true,
      sources: { platform: 'env', project: 'platform', fields: {} },
    });
    mocks.hasUsablePlatformAuth.mockResolvedValue(false);

    const stdout = vi.spyOn(console, 'log').mockImplementation(() => {});

    const exitCode = await executeCli(['config', 'show', '--json'], {
      cwd: path.join(isolatedHome, 'unregistered-project'),
    });

    const records = stdout.mock.calls.map(([record]) => String(record));
    expect(exitCode).toBe(1);
    expect(records).toHaveLength(1);
    expect(JSON.parse(records[0]!)).toMatchObject({
      ok: false,
      error: {
        code: 'KB_AUTH_REQUIRED',
        message: 'Authentication is required.',
        hint: 'Run "kb auth login --gateway-url <url> --email <email> --password <password>".',
      },
    });
    expect(mocks.loadPlatformConfiguration).toHaveBeenCalledExactlyOnceWith(
      path.join(isolatedHome, 'unregistered-project'),
      undefined,
      'platform',
    );
    expect(mocks.resolveProjectConfig).not.toHaveBeenCalled();
    expect(mocks.initializePlatform).not.toHaveBeenCalled();
    expect(mocks.tryResolveGateway).not.toHaveBeenCalled();
    expect(mocks.executeViaGateway).not.toHaveBeenCalled();
  });

  // case: S-203.2, S-047.3
  it('returns one JSON error envelope when a removed plugin command is unknown', async () => {
    isolatedHome = await mkdtemp(path.join(os.tmpdir(), 'kb-cli-secured-unknown-'));
    process.env.HOME = isolatedHome;
    process.env.KB_HOME = path.join(isolatedHome, '.kb');
    delete process.env.KB_PLATFORM_ROOT;
    delete process.env.KB_NET_OFFSET;

    const rawConfig = { gateway: { access: { mode: 'local' } } };
    mocks.loadPlatformConfiguration.mockResolvedValue({
      platformConfig: {}, rawConfig, rawPlatformConfig: rawConfig, effectiveConfig: undefined,
      platformRoot: isolatedHome, projectRoot: undefined, sameLocation: true,
      sources: { platform: 'env', project: 'platform', fields: {} },
    });
    mocks.initializePlatform.mockResolvedValue({
      platform: { logger: mocks.logger }, platformConfig: {}, rawConfig, effectiveConfig: rawConfig,
      platformRoot: isolatedHome, projectRoot: undefined,
    });
    mocks.hasUsablePlatformAuth.mockResolvedValue(false);

    const stdout = vi.spyOn(console, 'log').mockImplementation(() => {});
    const exitCode = await executeCli(['commit', 'open', '--json'], { cwd: isolatedHome });
    const records = stdout.mock.calls.map(([record]) => String(record));

    expect(exitCode).toBe(2);
    expect(JSON.parse(records[0]!)).toMatchObject({
      ok: false,
      error: { code: 'KB_RUNTIME_INPUT_INVALID', message: 'Unknown command: commit open' },
    });
    expect(mocks.hasUsablePlatformAuth).not.toHaveBeenCalled();
    expect(mocks.resolveProjectConfig).not.toHaveBeenCalled();
  });
});
