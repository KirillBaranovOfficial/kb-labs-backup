import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  AdapterUnavailableError,
  createErrorEnvelope,
  resolveCliGatewayTarget,
  serializeError,
  toErrorEnvelope,
  validateErrorEnvelope,
} from '@kb-labs/core-platform';
import type { ILogger } from '@kb-labs/core-platform';
import { PluginError } from '@kb-labs/plugin-contracts';
import * as pluginContracts from '@kb-labs/plugin-contracts';
import { registry, registerBuiltinCommands, resolveCliHandlerPath } from '@kb-labs/cli-commands';
import type { RegisteredCommand } from '@kb-labs/cli-commands';
import { fileURLToPath } from 'node:url';

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
    const matcher = vi.spyOn(pluginContracts, 'matchesPermissionPattern');
    executeCommandV3.mockResolvedValue(0);
    const previousGatewayUrl = process.env.KB_GATEWAY_URL;
    const manifestCmd = {
      pkgRoot: '/platform/plugins/marketplace',
      manifest: {
        id: '@kb-labs/marketplace-entry',
        segments: ['marketplace', 'plugins', 'doctor'],
        v3Manifest: {
        id: '@kb-labs/marketplace-entry',
        version: '1.0.0',
        permissions: { env: { read: ['KB_GATEWAY_URL'] } },
        cli: { commands: [{ path: 'marketplace plugins doctor', handler: 'dist/index.js#doctor' }] },
        },
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
      expect(matcher).toHaveBeenCalledWith('KB_GATEWAY_URL', 'KB_GATEWAY_URL');
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
      manifest: {
        id: '@kb-labs/marketplace-entry',
        segments: ['marketplace', 'plugins', 'doctor'],
        v3Manifest: {
        id: '@kb-labs/marketplace-entry',
        version: '1.0.0',
        permissions: { env: { read: ['KB_GATEWAY_URL'] } },
        cli: { commands: [{ path: 'marketplace plugins doctor', handler: 'dist/index.js#doctor' }] },
        },
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
      manifest: {
        id: '@kb-labs/marketplace-entry',
        segments: ['marketplace', 'plugins', 'doctor'],
        v3Manifest: {
        id: '@kb-labs/marketplace-entry',
        version: '1.0.0',
        permissions: { env: { read: ['KB_GATEWAY_URL'] } },
        cli: { commands: [{ path: 'marketplace plugins doctor', handler: 'dist/index.js#doctor' }] },
        },
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

  it('routes every registered bundled V3 handler through the execution runtime entry point', async () => {
    executeCommandV3.mockResolvedValue(0);
    const workspaceRoot = fileURLToPath(new URL('../../../..', import.meta.url));
    await registerBuiltinCommands({
      cwd: workspaceRoot,
      platformRoot: workspaceRoot,
      projectRoot: workspaceRoot,
      logger: {
        trace() {},
        debug() {},
        info() {},
        warn() {},
        error() {},
        fatal() {},
        child: () => noopLogger,
      } satisfies ILogger,
    });

    const handlers = registry.listCommands().flatMap((registered) => {
      const manifest = registered.manifest.v3Manifest;
      const commandPath = registered.manifest.segments.join(' ');
      const handler = manifest?.cli?.commands?.find((command) => command.path === commandPath);
      return handler ? [{ registered, commandPath, handler }] : [];
    });
    expect(handlers.length).toBeGreaterThan(0);

    const platform = {
      getSocketPath: () => '/tmp/plugin-execution-registry.sock',
      getAdapter: () => undefined,
    } as unknown as Parameters<typeof executePlugin>[0]['platform'];
    const context = {
      cwd: workspaceRoot,
      presenter: {},
    } as Parameters<typeof executePlugin>[0]['context'];

    for (const { registered, commandPath, handler } of handlers) {
      const exitCode = await executePlugin({
        context,
        commandId: commandPath.replaceAll(' ', ':'),
        argv: [],
        flags: {},
        manifestCmd: registered,
        platformRoot: workspaceRoot,
        platform,
      });
      expect(exitCode, `${registered.packageName} ${commandPath}`).toBe(0);
      expect(executeCommandV3).toHaveBeenLastCalledWith(expect.objectContaining({
        pluginRoot: registered.pkgRoot,
        handlerPath: resolveCliHandlerPath(registered.pkgRoot!, handler.handler),
      }));
    }

    expect(executeCommandV3).toHaveBeenCalledTimes(handlers.length);
  });

  it('serializes each injected failure class for every registered bundled V3 handler', async () => {
    const actualRuntime = await vi.importActual<typeof import('@kb-labs/cli-runtime/v3')>('@kb-labs/cli-runtime/v3');
    executeCommandV3.mockImplementation((options) => actualRuntime.executeCommandV3(options));
    const output = vi.spyOn(console, 'log').mockImplementation(() => {});
    const workspaceRoot = fileURLToPath(new URL('../../../..', import.meta.url));
    await registerBuiltinCommands({
      cwd: workspaceRoot,
      platformRoot: workspaceRoot,
      projectRoot: workspaceRoot,
      logger: {
        trace() {},
        debug() {},
        info() {},
        warn() {},
        error() {},
        fatal() {},
        child: () => noopLogger,
      } satisfies ILogger,
    });

    const handlers = registry.listCommands().flatMap((registered) => {
      const manifest = registered.manifest.v3Manifest;
      const commandPath = registered.manifest.segments.join(' ');
      const handler = manifest?.cli?.commands?.find((command) => command.path === commandPath);
      return handler ? [{ registered, commandPath, handler }] : [];
    });
    expect(handlers.length).toBeGreaterThan(0);

    const cases = [
      {
        name: 'plain Error',
        code: 'KB_PLUGIN_COMMAND_FAILED',
        buildEnvelope: (correlationId: string) => createErrorEnvelope('KB_PLUGIN_COMMAND_FAILED', {
          correlationId,
        }),
        error: () => new Error('registry plain failure'),
      },
      {
        name: 'typed product error',
        code: 'KB_PLUGIN_PERMISSION_DENIED',
        buildEnvelope: (correlationId: string) => toErrorEnvelope(
          new PluginError('registry typed product failure', 'KB_PLUGIN_PERMISSION_DENIED'),
          { correlationId },
        ),
        error: () => new PluginError('registry typed product failure', 'KB_PLUGIN_PERMISSION_DENIED'),
      },
      {
        name: 'platform adapter error',
        code: 'KB_ADAPTER_NOT_CONFIGURED',
        buildEnvelope: (correlationId: string) => toErrorEnvelope(new AdapterUnavailableError('llm'), { correlationId }),
        error: () => new AdapterUnavailableError('llm'),
      },
    ] as const;

    for (const failureCase of cases) {
      for (const { registered, commandPath, handler } of handlers) {
        output.mockClear();
        const platform = {
          getSocketPath: () => '/tmp/plugin-execution-registry.sock',
          getAdapter: () => undefined,
          executionBackend: {
            async execute(request: { descriptor: { requestId: string }; context?: { commandPath?: string } }) {
              const error = failureCase.error();
              const correlationId = request.descriptor.requestId;
              const envelope = failureCase.buildEnvelope(correlationId);
              return {
                ok: false as const,
                error: { ...envelope, error: serializeError(error), envelope },
                executionTimeMs: 1,
              };
            },
          },
        } as unknown as Parameters<typeof executePlugin>[0]['platform'];

        const exitCode = await executePlugin({
          context: {
            cwd: workspaceRoot,
            presenter: {},
          } as Parameters<typeof executePlugin>[0]['context'],
          commandId: commandPath.replaceAll(' ', ':'),
          argv: [],
          flags: { json: true },
          manifestCmd: registered,
          platformRoot: workspaceRoot,
          platform,
        });

        expect(exitCode, `${failureCase.name}: ${registered.packageName} ${commandPath}`).toBe(1);
        expect(output).toHaveBeenCalledOnce();
        const serialized = JSON.parse(String(output.mock.calls[0]?.[0])) as { ok: false; error: Record<string, unknown> };
        expect(serialized.ok).toBe(false);
        expect(serialized.error.code).toBe(failureCase.code);
        expect(serialized.error.correlationId).toEqual(expect.any(String));
        expect(serialized.error).not.toHaveProperty('stack');
        expect(validateErrorEnvelope(serialized.error)).toEqual([]);
        expect(executeCommandV3).toHaveBeenLastCalledWith(expect.objectContaining({
          pluginRoot: registered.pkgRoot,
          handlerPath: resolveCliHandlerPath(registered.pkgRoot!, handler.handler),
        }));
      }
    }

    executeCommandV3.mockReset();
    output.mockRestore();
  });
});

const noopLogger: ILogger = {
  trace() {},
  debug() {},
  info() {},
  warn() {},
  error() {},
  fatal() {},
  child: () => noopLogger,
};
