import { afterEach, describe, expect, it, vi } from 'vitest';

const gatewayApiFetch = vi.hoisted(() => vi.fn());
vi.mock('@kb-labs/sdk', () => ({ useEnv: (name: string) => name === 'KB_GATEWAY_URL' ? 'http://gateway.test' : undefined }));
vi.mock('@kb-labs/cli-runtime/gateway', () => ({ gatewayApiFetch }));

import { get } from '../http.js';

describe('marketplace shared gateway client', () => {
  afterEach(() => { gatewayApiFetch.mockReset(); });

  // case: S-041.7
  it('delegates marketplace REST calls to the shared authenticated client', async () => {
    gatewayApiFetch.mockResolvedValue(new Response(JSON.stringify({ ok: true }), { status: 200 }));
    await get('/packages');
    expect(gatewayApiFetch).toHaveBeenCalledWith(
      new URL('http://gateway.test/api/v1/marketplace/packages'),
      expect.objectContaining({ method: 'GET' }),
    );
  });

  it('renders product errors without leaking raw response JSON', async () => {
    gatewayApiFetch.mockResolvedValue(new Response(JSON.stringify({
      ok: false,
      error: { code: 'KB_PLUGIN_NOT_FOUND', message: 'Plugin was not found', hint: 'Install it with kb marketplace install <package>.' },
      internal: 'secret detail',
    }), { status: 404 }));
    await expect(get('/packages')).rejects.toThrow(
      'Marketplace request failed (404): Plugin was not found. Next: Install it with kb marketplace install <package>.',
    );
  });

  it('points timeout recovery at the installed platform commands', async () => {
    const abort = new Error('aborted');
    abort.name = 'AbortError';
    gatewayApiFetch.mockRejectedValue(abort);
    await expect(get('/packages')).rejects.toThrow(
      'Marketplace /packages timed out. Check the installed platform with `kb-create status`; start it with `kb-create start` if it is stopped.',
    );
  });
});
