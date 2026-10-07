import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AdapterUnavailableError, resolveCliGatewayTarget } from '@kb-labs/core-platform';
import type { RegisteredCommand } from '@kb-labs/cli-commands';

const { executeCommandV3, resolveActiveGateway } = vi.hoisted(() => ({ executeCommandV3: vi.fn(), resolveActiveGateway: vi.fn() }));
vi.mock('@kb-labs/cli-runtime/v3', () => ({ executeCommandV3 }));
vi.mock('@kb-labs/cli-runtime/gateway', () => ({ resolveActiveGateway }));

import { executePlugin, renderUnavailableAdapterError } from './plugin-executor.js';

describe('plugin executor adapter errors', () => {
  afterEach(() => vi.restoreAllMocks());

  beforeEach(() => {
    resolveActiveGateway.mockImplementation(({ platformRoot, env, configuredNetOffset, transport }) => resolveCliGatewayTarget(platformRoot, {
      gatewayUrl: env.KB_GATEWAY_URL,
      gatewayPort: env.GATEWAY_PORT,
      gatewayHost: env.GATEWAY_HOST,
      netOffset: env.KB_NET_OFFSET === undefined ? configuredNetOffset ?? 0 : Number(env.KB_NET_OFFSET),
    }, transport));
  });

  it('renders an unconfigured capability as one line and marks it handled', () => {
    const errorOutput = vi.spyOn(console, 'error').mockImplementation(() => {});

    const handled = renderUnavailableAdapterError(new AdapterUnavailableError('llm'));

    expect(handled).toBe(true);
    expect(errorOutput).toHaveBeenCalledOnce();
    expect(errorOutput.mock.calls[0]?.[0]).toBe(
      'Service llm is not configured. Configure it with: kb adapter bind llm @kb-labs/adapters-openai',
    );
    expect(errorOutput.mock.calls[0]?.[0]).not.toContain(' at ');
  });

  it('leaves other errors to the regular plugin execution error path', () => {
    expect(renderUnavailableAdapterError(new Error('other failure'))).toBe(false);
  });

  // case: S-010.7
  it('passes the configured gateway address to the plugin backend without changing process.env', async () => {
    executeCommandV3.mockResolvedValue(0);
    const previousGatewayUrl = process.env.KB_GATEWAY_URL;
    const manifestCmd = {
      pkgRoot: '/platform/plugins/marketplace',
      manifest: { id: '@kb-labs/marketplace-entry', segments: ['marketplace', 'plugins', 'doctor'] },
      v3Manifest: {
        id: '@kb-labs/marketplace-entry',
        version: '1.0.0',
        permissions: { env: { read: ['KB_GATEWAY_URL'] } },
        cli: { commands: [{ path: 'marketplace plugins doctor', handler: 'dist/index.js#doctor' }] },
      },
    } as unknown as RegisteredCommand;
    const context = {
      cwd: '/project',
      presenter: {},
      logger: undefined,
    } as Parameters<typeof executePlugin>[0]['context'];
    const platform = {
      getSocketPath: () => '/tmp/platform.sock',
      getAdapter: () => ({ listenAddress: () => ({ host: '127.0.0.1', port: 43323 }) }),
    } as unknown as Parameters<typeof executePlugin>[0]['platform'];

    try {
      const exitCode = await executePlugin({
        context,
        netOffset: 200,
        commandId: 'marketplace:plugins:doctor',
        argv: [],
        flags: {},
        manifestCmd,
        platformRoot: "/platform",
        platform,
      });

      expect(exitCode).toBe(0);
      expect(executeCommandV3).toHaveBeenCalledWith(expect.objectContaining({
        env: { KB_GATEWAY_URL: 'http://127.0.0.1:43323' },
      }));
      expect(process.env.KB_GATEWAY_URL).toBe(previousGatewayUrl);
    } finally {
      executeCommandV3.mockReset();
    }
  });

  // case: S-203.15
  it('resolves the default gateway address when fresh platform config omits gateway.port', async () => {
    executeCommandV3.mockResolvedValue(0);
    const previousGatewayPort = process.env.GATEWAY_PORT;
    delete process.env.GATEWAY_PORT;
    const manifestCmd = {
      pkgRoot: '/platform/plugins/marketplace',
      manifest: { id: '@kb-labs/marketplace-entry', segments: ['marketplace', 'plugins', 'doctor'] },
      v3Manifest: {
        id: '@kb-labs/marketplace-entry',
        version: '1.0.0',
        permissions: { env: { read: ['KB_GATEWAY_URL'] } },
        cli: { commands: [{ path: 'marketplace plugins doctor', handler: 'dist/index.js#doctor' }] },
      },
    } as unknown as RegisteredCommand;

    try {
      const exitCode = await executePlugin({
        context: { cwd: '/project', presenter: {} } as Parameters<typeof executePlugin>[0]['context'],
        commandId: 'marketplace:plugins:doctor',
        argv: [],
        flags: {},
        manifestCmd,
        platformRoot: "/platform",
        netOffset: 200,
        platform: {
          getSocketPath: () => '/tmp/platform.sock',
          getAdapter: () => ({ listenAddress: () => undefined }),
        } as unknown as Parameters<typeof executePlugin>[0]['platform'],
      });

      expect(exitCode).toBe(0);
      expect(executeCommandV3).toHaveBeenCalledWith(expect.objectContaining({
        env: { KB_GATEWAY_URL: 'http://127.0.0.1:4200' },
      }));
    } finally {
      if (previousGatewayPort === undefined) delete process.env.GATEWAY_PORT;
      else process.env.GATEWAY_PORT = previousGatewayPort;
      executeCommandV3.mockReset();
    }
  });

  // case: S-203.16
  it('rejects an invalid GATEWAY_PORT before starting the plugin', async () => {
    const previousGatewayPort = process.env.GATEWAY_PORT;
    process.env.GATEWAY_PORT = '4000oops';
    const manifestCmd = {
      pkgRoot: '/platform/plugins/marketplace',
      manifest: { id: '@kb-labs/marketplace-entry', segments: ['marketplace', 'plugins', 'doctor'] },
      v3Manifest: {
        id: '@kb-labs/marketplace-entry',
        version: '1.0.0',
        permissions: { env: { read: ['KB_GATEWAY_URL'] } },
        cli: { commands: [{ path: 'marketplace plugins doctor', handler: 'dist/index.js#doctor' }] },
      },
    } as unknown as RegisteredCommand;

    try {
      await expect(executePlugin({
        context: { cwd: '/project', presenter: {} } as Parameters<typeof executePlugin>[0]['context'],
        commandId: 'marketplace:plugins:doctor',
        argv: [],
        flags: {},
        manifestCmd,
        platformRoot: "/platform",
        netOffset: 0,
        platform: {
          getSocketPath: () => '/tmp/platform.sock',
          getAdapter: () => ({ listenAddress: () => undefined }),
        } as unknown as Parameters<typeof executePlugin>[0]['platform'],
      })).rejects.toMatchObject({ name: 'ServicePortConfigError', envVar: 'GATEWAY_PORT' });
      expect(executeCommandV3).not.toHaveBeenCalled();
    } finally {
      if (previousGatewayPort === undefined) delete process.env.GATEWAY_PORT;
      else process.env.GATEWAY_PORT = previousGatewayPort;
      executeCommandV3.mockReset();
    }
  });
});
