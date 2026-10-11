import { describe, expect, it, vi } from 'vitest';
import { noopUI } from '@kb-labs/plugin-contracts';
import type { PlatformServices } from '@kb-labs/plugin-contracts';
import type { PlatformContainer } from '@kb-labs/core-runtime';
import type { ExecutionRequest } from '@kb-labs/plugin-execution-factory';
import { toErrorEnvelope } from '@kb-labs/core-platform';
import { executeCommandV3 } from './execute-command.js';

describe('executeCommandV3 environment', () => {
  // case: S-010.7
  it('passes the CLI-resolved gateway address to the execution backend without mutating process.env', async () => {
    const previousGatewayUrl = process.env.KB_GATEWAY_URL;
    const execute = vi.fn(async (request: ExecutionRequest) => {
      expect(request.env).toEqual({ KB_GATEWAY_URL: 'http://127.0.0.1:43123' });
      return { ok: true, data: null, executionTimeMs: 0 };
    });
    const platformContainer = {
      executionBackend: { execute },
    } as unknown as PlatformContainer;

    await executeCommandV3({
      pluginId: '@kb-labs/test-plugin',
      pluginVersion: '1.0.0',
      pluginRoot: '/tmp/test-plugin',
      handlerPath: 'dist/commands/test.js',
      argv: [],
      flags: {},
      env: { KB_GATEWAY_URL: 'http://127.0.0.1:43123' },
      ui: noopUI,
      platform: {} as unknown as PlatformServices,
      platformContainer,
    });

    expect(execute).toHaveBeenCalledOnce();
    expect(process.env.KB_GATEWAY_URL).toBe(previousGatewayUrl);
  });
});

describe('executeCommandV3 plugin command result output', () => {
  it.each([
    ['catalogued', Object.assign(new Error('Catalogued failure.'), {
      code: 'KB_RUNTIME_COMMAND_FAILED',
      hint: 'Fix the failure and retry.',
      details: { resource: 'widget-1' },
    })],
    ['plain Error', new Error('Unexpected failure.')],
    ['string', 'Unexpected string failure'],
    ['invalid details', Object.assign(new Error('Bad details.'), {
      code: 'KB_RUNTIME_COMMAND_FAILED',
      details: { report: '{"errors":[]}' },
    })],
    ['non-string details', Object.assign(new Error('Bad detail type.'), {
      code: 'KB_RUNTIME_COMMAND_FAILED',
      details: { report: 42 },
    })],
  ])('normalizes thrown %s through the platform error converter', async (_name, thrown) => {
    const json = vi.fn();
    const execute = vi.fn(async () => { throw thrown; });
    const platformContainer = { executionBackend: { execute } } as unknown as PlatformContainer;

    const exitCode = await executeCommandV3({
      pluginId: '@kb-labs/test-plugin',
      pluginVersion: '1.0.0',
      handlerPath: 'dist/commands/test.js',
      argv: [],
      flags: { json: true },
      ui: { ...noopUI, json },
      platform: { logger: { error: vi.fn() } } as unknown as PlatformServices,
      platformContainer,
    });

    expect(exitCode).toBe(1);
    expect(json).toHaveBeenCalledOnce();
    const envelope = (json.mock.calls[0]?.[0] as { error: { correlationId?: string } }).error;
    expect(envelope).toEqual(toErrorEnvelope(thrown, { correlationId: envelope.correlationId }));
  });

  // case: S-203.1, S-203.2
  it('renders a returned typed failure once as the shared envelope in JSON mode', async () => {
    const json = vi.fn();
    const error = vi.fn();
    const execute = vi.fn(async () => ({
      ok: true as const,
      data: {
        ok: false as const,
        error: {
          code: 'KB_RUNTIME_COMMAND_FAILED',
          message: 'The operation failed.',
          hint: 'Resolve the reported command failure, then run the command again.',
          details: { resource: 'record-17' },
        },
      },
      executionTimeMs: 1,
    }));
    const platformContainer = { executionBackend: { execute } } as unknown as PlatformContainer;

    const exitCode = await executeCommandV3({
      pluginId: '@kb-labs/test-plugin',
      pluginVersion: '1.0.0',
      handlerPath: 'dist/commands/test.js',
      argv: [],
      flags: { json: true },
      ui: { ...noopUI, json, error },
      platform: {} as unknown as PlatformServices,
      platformContainer,
    });

    expect(exitCode).toBe(1);
    expect(json).toHaveBeenCalledOnce();
    expect(json).toHaveBeenCalledWith(expect.objectContaining({
      ok: false,
      error: expect.objectContaining({
        code: 'KB_RUNTIME_COMMAND_FAILED',
        message: 'The operation failed.',
        hint: 'Resolve the reported command failure, then run the command again.',
        details: { resource: 'record-17' },
      }),
    }));
    expect(error).not.toHaveBeenCalled();
  });

  // case: S-203.2
  it('preserves nested plain data that resembles a legacy string failure', async () => {
    const json = vi.fn();
    const execute = vi.fn(async () => ({
      ok: true as const,
      data: { payload: { ok: false, error: 'free text failure' } },
      executionTimeMs: 1,
    }));
    const platformContainer = { executionBackend: { execute } } as unknown as PlatformContainer;

    const exitCode = await executeCommandV3({
      pluginId: '@kb-labs/test-plugin',
      pluginVersion: '1.0.0',
      handlerPath: 'dist/commands/test.js',
      argv: [],
      flags: { json: true },
      ui: { ...noopUI, json },
      platform: {} as unknown as PlatformServices,
      platformContainer,
    });

    expect(exitCode).toBe(0);
    expect(json).toHaveBeenCalledOnce();
    expect(json).toHaveBeenCalledWith({
      ok: true,
      data: { payload: { ok: false, error: 'free text failure' } },
      meta: expect.any(Object),
    });
  });

  // case: S-203.2, S-051.2
  it('renders a returned typed failure as the shared human error with its hint', async () => {
    const json = vi.fn();
    const error = vi.fn();
    const execute = vi.fn(async () => ({
      ok: true as const,
      data: {
        ok: false as const,
        error: {
          code: 'KB_PLUGIN_NOT_FOUND',
          message: 'Plugin @kb-labs/not-published was not found in the registry.',
          hint: 'kb marketplace search',
        },
      },
      executionTimeMs: 1,
    }));
    const platformContainer = { executionBackend: { execute } } as unknown as PlatformContainer;

    const exitCode = await executeCommandV3({
      pluginId: '@kb-labs/test-plugin',
      pluginVersion: '1.0.0',
      handlerPath: 'dist/commands/test.js',
      argv: [],
      flags: {},
      ui: { ...noopUI, json, error },
      platform: {} as unknown as PlatformServices,
      platformContainer,
    });

    expect(exitCode).toBe(1);
    expect(json).not.toHaveBeenCalled();
    expect(error).toHaveBeenCalledOnce();
    expect(error).toHaveBeenCalledWith(expect.stringMatching(/^Plugin @kb-labs\/not-published was not found in the registry\. \[KB_PLUGIN_NOT_FOUND\]\n  Hint:  kb marketplace search\n  Correlation ID: cli-[0-9]+-[a-z0-9]+$/), {
      hint: 'kb marketplace search',
    });
  });

  // case: S-203.2
  it('keeps a catalogued execution error code and its message', async () => {
    const json = vi.fn();
    const execute = vi.fn(async () => ({
      ok: false as const,
      error: { code: 'KB_PLUGIN_NOT_FOUND', message: 'Plugin @acme/missing was not found.', hint: 'Install the plugin and retry.', correlationId: 'worker-correlation-17', details: { plugin: '@acme/missing' } },
      executionTimeMs: 1,
    }));
    const platform = { logger: { error: vi.fn() } } as unknown as PlatformServices;
    const platformContainer = { executionBackend: { execute } } as unknown as PlatformContainer;

    const exitCode = await executeCommandV3({
      pluginId: '@kb-labs/test-plugin', pluginVersion: '1.0.0', handlerPath: 'dist/commands/test.js',
      argv: [], flags: { json: true }, ui: { ...noopUI, json }, platform, platformContainer,
    });

    expect(exitCode).toBe(1);
    expect(json).toHaveBeenCalledWith(expect.objectContaining({
      ok: false,
      error: expect.objectContaining({
        code: 'KB_PLUGIN_NOT_FOUND',
        message: 'Plugin @acme/missing was not found.',
        hint: expect.any(String),
        correlationId: 'worker-correlation-17',
        details: { plugin: '@acme/missing' },
      }),
    }));
    expect(platform.logger.error).not.toHaveBeenCalled();
  });

  // case: S-203.3
  it('renders thrown uncatalogued errors as unexpected and logs the cause with its correlation ID', async () => {
    const json = vi.fn();
    const logError = vi.fn();
    const execute = vi.fn(async () => { throw new Error('private worker cause'); });
    const platform = { logger: { error: logError } } as unknown as PlatformServices;
    const platformContainer = { executionBackend: { execute } } as unknown as PlatformContainer;

    const exitCode = await executeCommandV3({
      pluginId: '@kb-labs/test-plugin', pluginVersion: '1.0.0', handlerPath: 'dist/commands/test.js',
      argv: [], flags: { json: true }, ui: { ...noopUI, json }, platform, platformContainer,
    });

    const envelope = json.mock.calls[0]?.[0] as { error: { code: string; correlationId: string; message: string } };
    expect(exitCode).toBe(1);
    expect(envelope.error.code).toBe('KB_RUNTIME_UNEXPECTED');
    expect(envelope.error.message).not.toContain('private worker cause');
    expect(logError).toHaveBeenCalledWith('Plugin command failed unexpectedly', expect.any(Error), {
      correlationId: envelope.error.correlationId,
      requestId: expect.any(String),
    });
  });
});
