import { afterEach, describe, expect, it, vi } from 'vitest';
import * as contracts from '@kb-labs/plugin-contracts';
import { createEnvShim } from '../runtime/env-shim.js';
import { createInvokeAPI, type PluginInvokerFn } from '../api/invoke.js';

describe('plugin runtime permission pattern delegation', () => {
  afterEach(() => vi.restoreAllMocks());

  it('uses the contract matcher for environment and invoke scopes', async () => {
    const matcher = vi.spyOn(contracts, 'matchesPermissionPattern');
    const env = createEnvShim({
      permissions: { env: { read: ['KB_*'] }, invoke: { allow: ['@kb-labs/*'] } },
      environment: { KB_GATEWAY_URL: 'http://127.0.0.1' },
    });
    expect(env('KB_GATEWAY_URL')).toBe('http://127.0.0.1');

    const invocationIds: string[] = [];
    const invoker: PluginInvokerFn = async <T>(pluginId: string) => {
      invocationIds.push(pluginId);
      return undefined as T;
    };
    const invoke = createInvokeAPI({
      permissions: { invoke: { allow: ['@kb-labs/*'] } },
      invoker,
    });
    await invoke.call('@kb-labs/example');

    expect(matcher).toHaveBeenCalledWith('KB_GATEWAY_URL', 'KB_*');
    expect(matcher).toHaveBeenCalledWith('@kb-labs/example', '@kb-labs/*');
    expect(invocationIds).toEqual(['@kb-labs/example']);
  });
});
