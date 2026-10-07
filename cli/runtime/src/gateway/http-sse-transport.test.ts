import { afterEach, describe, expect, it, vi } from 'vitest';
import { HttpSseGatewayTransport } from './http-sse-transport.js';
import type { GatewayCredentials, ICredentialsManager } from './types.js';

describe('HttpSseGatewayTransport credential origin', () => {
  afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

  // case: S-203.23
  it('does not refresh or send machine credentials to another Gateway origin', async () => {
    const credentials: GatewayCredentials = {
      gatewayUrl: 'https://gateway-a.test',
      accessToken: 'gateway-a-access',
      refreshToken: 'gateway-a-refresh',
      expiresAt: 0,
    };
    const refresh = vi.fn(async () => credentials);
    const manager: ICredentialsManager = {
      load: async () => credentials,
      save: async () => undefined,
      clear: async () => undefined,
      isExpired: () => true,
      refresh,
    };
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    const transport = new HttpSseGatewayTransport({ gatewayUrl: 'https://gateway-b.test' }, manager);

    const execution = (async () => {
      for await (const event of transport.execute({ pluginId: '@example/plugin', handlerRef: 'dist/index.js', input: {} })) { void event; }
    })();
    await expect(execution)
      .rejects.toMatchObject({ envelope: { code: 'KB_AUTH_REQUIRED', details: { gatewayUrl: 'https://gateway-b.test' } } });

    expect(fetch).not.toHaveBeenCalled();
    expect(refresh).not.toHaveBeenCalled();
  });
});
