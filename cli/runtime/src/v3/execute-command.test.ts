import { describe, expect, it, vi } from 'vitest';
import { noopUI } from '@kb-labs/plugin-contracts';
import type { PlatformServices } from '@kb-labs/plugin-contracts';
import type { PlatformContainer } from '@kb-labs/core-runtime';
import type { ExecutionRequest } from '@kb-labs/plugin-execution-factory';
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
