import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { resolveCliGatewayTarget } from '@kb-labs/core-platform';

const mocks = vi.hoisted(() => ({
  loadPlatformConfiguration: vi.fn(),
  resolveProjectConfig: vi.fn(),
  initializePlatform: vi.fn(),
  hasUsablePlatformAuth: vi.fn(),
  resolveActiveGateway: vi.fn(),
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
vi.mock('@kb-labs/cli-runtime/gateway', async (importOriginal) => ({
  ...await importOriginal<typeof import('@kb-labs/cli-runtime/gateway')>(),
  CredentialsManager: class { async load() { return null; } },
  hasUsablePlatformAuth: mocks.hasUsablePlatformAuth,
  resolveActiveGateway: mocks.resolveActiveGateway,
}));
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
  const originalGatewayPort = process.env.GATEWAY_PORT;
  const originalGatewayUrl = process.env.KB_GATEWAY_URL;
  const originalLogStream = process.env.KB_LOG_STREAM;
  const originalOutputMode = process.env.KB_OUTPUT_MODE;

  afterEach(async () => {
    if (isolatedHome) await rm(isolatedHome, { recursive: true, force: true });
    isolatedHome = undefined;
    process.env.HOME = originalHome;
    process.env.KB_HOME = originalKbHome;
    process.env.KB_PLATFORM_ROOT = originalPlatformRoot;
    process.env.KB_NET_OFFSET = originalNetOffset;
    process.env.GATEWAY_PORT = originalGatewayPort;
    process.env.KB_GATEWAY_URL = originalGatewayUrl;
    process.env.KB_LOG_STREAM = originalLogStream;
    process.env.KB_OUTPUT_MODE = originalOutputMode;
    vi.clearAllMocks();
  });

  beforeEach(() => {
    mocks.resolveActiveGateway.mockImplementation(({ platformRoot, env }) => resolveCliGatewayTarget(platformRoot, {
      gatewayUrl: env.KB_GATEWAY_URL,
      gatewayPort: env.GATEWAY_PORT,
      gatewayHost: env.GATEWAY_HOST,
      netOffset: Number(env.KB_NET_OFFSET ?? 0),
    }));
  });

  // case: S-031.1, S-041.8
  it('lets auth status report an expired saved session without refreshing it first', async () => {
    isolatedHome = await mkdtemp(path.join(os.tmpdir(), 'kb-cli-expired-session-'));
    process.env.HOME = isolatedHome;
    process.env.KB_HOME = path.join(isolatedHome, '.kb');
    process.env.KB_GATEWAY_URL = 'http://gateway.test';
    process.env.GATEWAY_PORT = '4300';
    delete process.env.KB_PLATFORM_ROOT;
    await mkdir(path.join(isolatedHome, '.kb'), { recursive: true });
    const sessionPath = path.join(isolatedHome, '.kb', 'session.json');
    await writeFile(sessionPath, JSON.stringify({
      gatewayUrl: 'http://gateway.test', accessToken: 'expired-access', refreshToken: 'valid-refresh',
      expiresAt: 0, email: 'member@example.test',
    }));
    mocks.loadPlatformConfiguration.mockResolvedValue({
      platformConfig: {}, rawConfig: { gateway: { access: { mode: 'secured' } } },
      rawPlatformConfig: { gateway: { access: { mode: 'secured' } } }, effectiveConfig: undefined,
      platformRoot: isolatedHome, projectRoot: undefined, sameLocation: true,
      sources: { platform: 'env', project: 'platform', fields: {} },
    });
    mocks.initializePlatform.mockResolvedValue({
      platform: { logger: mocks.logger }, platformConfig: {},
      rawConfig: { gateway: { access: { mode: 'secured' } } }, effectiveConfig: {},
      platformRoot: isolatedHome, projectRoot: undefined,
    });
    mocks.hasUsablePlatformAuth.mockImplementation(async () => {
      await writeFile(sessionPath, JSON.stringify({
        gatewayUrl: 'http://gateway.test', accessToken: 'refreshed-access', refreshToken: 'next-refresh',
        expiresAt: Date.now() + 900_000, email: 'member@example.test',
      }));
      return { authenticated: true };
    });
    const stdout = vi.spyOn(console, 'log').mockImplementation(() => {});

    const exitCode = await executeCli(['auth', 'status', '--json'], { cwd: isolatedHome });
    const records = stdout.mock.calls.map(([record]) => String(record));

    expect(exitCode).toBe(0);
    expect(records).toHaveLength(1);
    expect(JSON.parse(records[0]!).data.session).toMatchObject({
      authenticated: true, tokenExpired: true, email: 'member@example.test',
    });
    expect(mocks.hasUsablePlatformAuth).not.toHaveBeenCalled();
  });

  // case: S-041.7, S-041.17, S-203.15
  it('returns the auth envelope from an unregistered project directory before project resolution', async () => {
    isolatedHome = await mkdtemp(path.join(os.tmpdir(), 'kb-cli-secured-auth-'));
    process.env.HOME = isolatedHome;
    process.env.KB_HOME = path.join(isolatedHome, '.kb');
    delete process.env.KB_GATEWAY_URL;
    delete process.env.KB_PLATFORM_ROOT;
    process.env.GATEWAY_PORT = '4300';
    process.env.KB_NET_OFFSET = '200';

    mocks.loadPlatformConfiguration.mockResolvedValue({
      platformConfig: {},
      rawConfig: {
        gateway: { access: { mode: 'secured' } },
        platform: { adapterOptions: { serviceTransport: { services: { gateway: { url: 'http://127.0.0.1:4000' } } } } },
      },
      rawPlatformConfig: { gateway: { access: { mode: 'secured' } } },
      effectiveConfig: undefined,
      platformRoot: isolatedHome,
      projectRoot: isolatedHome,
      sameLocation: true,
      sources: { platform: 'env', project: 'platform', fields: {} },
    });
    mocks.hasUsablePlatformAuth.mockResolvedValue({ authenticated: false });

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
        hint: 'Run "kb auth login --gateway-url http://127.0.0.1:4500 --email <email>" and enter your password at the prompt, or provide it with --password-stdin.',
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

  // case: S-203.16
  it('returns the catalogued GATEWAY_PORT config error before the auth hint', async () => {
    isolatedHome = await mkdtemp(path.join(os.tmpdir(), 'kb-cli-invalid-gateway-port-'));
    process.env.HOME = isolatedHome;
    process.env.KB_HOME = path.join(isolatedHome, '.kb');
    process.env.GATEWAY_PORT = '4000oops';
    delete process.env.KB_GATEWAY_URL;
    delete process.env.KB_PLATFORM_ROOT;
    delete process.env.KB_NET_OFFSET;

    const rawConfig = { gateway: { access: { mode: 'secured' } } };
    mocks.loadPlatformConfiguration.mockResolvedValue({
      platformConfig: {}, rawConfig, rawPlatformConfig: rawConfig, effectiveConfig: undefined,
      platformRoot: isolatedHome, projectRoot: undefined, sameLocation: true,
      sources: { platform: 'env', project: 'platform', fields: {} },
    });
    mocks.hasUsablePlatformAuth.mockResolvedValue({ authenticated: false });
    const stdout = vi.spyOn(console, 'log').mockImplementation(() => {});

    const exitCode = await executeCli(['config', 'show', '--json'], { cwd: isolatedHome });
    const records = stdout.mock.calls.map(([record]) => String(record));

    expect(exitCode).toBe(1);
    expect(records).toHaveLength(1);
    expect(JSON.parse(records[0]!)).toMatchObject({
      ok: false,
      error: { code: 'KB_CONFIG_INVALID', message: expect.stringContaining('GATEWAY_PORT') },
    });
    expect(mocks.hasUsablePlatformAuth).not.toHaveBeenCalled();
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
