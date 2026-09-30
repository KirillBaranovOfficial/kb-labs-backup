import { beforeEach, describe, expect, it, vi } from 'vitest';

const sessionLoad = vi.fn();
const sessionRefresh = vi.fn();
const sessionIsExpired = vi.fn();
const credentialsLoad = vi.fn();
const credentialsRefresh = vi.fn();
const credentialsIsExpired = vi.fn();

vi.mock('@kb-labs/sdk', () => ({
  useEnv: vi.fn((name: string) => name === 'KB_GATEWAY_URL' ? 'http://gateway.test' : undefined),
}));

vi.mock('@kb-labs/cli-runtime/gateway', () => ({
  SessionManager: class {
    load = sessionLoad;
    refresh = sessionRefresh;
    isExpired = sessionIsExpired;
  },
  CredentialsManager: class {
    load = credentialsLoad;
    refresh = credentialsRefresh;
    isExpired = credentialsIsExpired;
  },
}));

import { get } from '../http.js';

describe('marketplace HTTP authentication', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sessionLoad.mockResolvedValue(null);
    credentialsLoad.mockResolvedValue(null);
    sessionIsExpired.mockReturnValue(false);
    credentialsIsExpired.mockReturnValue(false);
  });

  it('forwards the logged-in session token', async () => {
    sessionLoad.mockResolvedValue({
      gatewayUrl: 'http://session-gateway.test',
      accessToken: 'session-token',
      refreshToken: 'session-refresh',
      expiresAt: Date.now() + 60_000,
    });
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ ok: true }), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);

    await get('/packages');

    expect(fetchMock).toHaveBeenCalledWith(
      'http://session-gateway.test/api/v1/marketplace/packages',
      expect.objectContaining({ headers: { Authorization: 'Bearer session-token' } }),
    );
  });

  it('refreshes an expired session and retries the request once', async () => {
    const expired = {
      gatewayUrl: 'http://gateway.test',
      accessToken: 'expired-token',
      refreshToken: 'refresh-token',
      expiresAt: Date.now() - 1,
    };
    const refreshed = { ...expired, accessToken: 'fresh-token', expiresAt: Date.now() + 60_000 };
    sessionLoad.mockResolvedValue(expired);
    sessionIsExpired.mockReturnValue(true);
    sessionRefresh.mockResolvedValue(refreshed);
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response('unauthorized', { status: 401 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ ok: true }), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);

    await get('/packages');

    expect(sessionRefresh).toHaveBeenCalledTimes(2);
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      'http://gateway.test/api/v1/marketplace/packages',
      expect.objectContaining({ headers: { Authorization: 'Bearer fresh-token' } }),
    );
  });

  it('gives a first-run instruction when no auth exists', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('refreshToken is required', { status: 401 }));
    vi.stubGlobal('fetch', fetchMock);

    await expect(get('/packages')).rejects.toThrow(
      'Marketplace authentication is required. Run `kb auth login` or choose local no-auth mode during installation.',
    );
    expect(sessionRefresh).not.toHaveBeenCalled();
    expect(credentialsRefresh).not.toHaveBeenCalled();
  });

  it('renders marketplace error envelopes without leaking raw response JSON', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      ok: false,
      error: { code: 'KB_PLUGIN_NOT_FOUND', message: 'Plugin was not found', hint: 'Install it with kb marketplace install <package>.' },
      internal: 'secret detail',
    }), { status: 404 }));
    vi.stubGlobal('fetch', fetchMock);

    await expect(get('/packages')).rejects.toThrow(
      'Marketplace request failed (404): Plugin was not found. Next: Install it with kb marketplace install <package>.',
    );
    await expect(get('/packages')).rejects.not.toThrow('secret detail');
  });

  it('points timeout recovery at the installed platform commands', async () => {
    const abort = new Error('aborted');
    abort.name = 'AbortError';
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(abort));

    await expect(get('/packages')).rejects.toThrow(
      'Marketplace /packages timed out. Check the installed platform with `kb-create status`; start it with `kb-create start` if it is stopped.',
    );
  });
});
