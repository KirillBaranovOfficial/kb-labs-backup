import { afterEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  session: { gatewayUrl: 'https://gateway.test', accessToken: 'saved', refreshToken: 'refresh', expiresAt: 0 },
  load: vi.fn(), refresh: vi.fn(),
}));

vi.mock('./session.js', () => ({
  SessionManager: class {
    async load() { return mocks.load(); }
    isExpired() { return true; }
    async refresh() { return mocks.refresh(); }
  },
}));
vi.mock('./credentials.js', () => ({ CredentialsManager: class { async load() { return null; } } }));

import { hasUsablePlatformAuth } from './platform-auth.js';

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

describe('platform auth verification cache', () => {
  it('loads, refreshes, and verifies the session once for the process', async () => {
    mocks.load.mockResolvedValue(mocks.session);
    mocks.refresh.mockResolvedValue({ ...mocks.session, accessToken: 'refreshed' });
    const fetchMock = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);

    const results = await Promise.all([hasUsablePlatformAuth(), hasUsablePlatformAuth()]);

    expect(results).toEqual([true, true]);
    expect(mocks.load).toHaveBeenCalledOnce();
    expect(mocks.refresh).toHaveBeenCalledOnce();
    expect(fetchMock).toHaveBeenCalledOnce();
    expect(fetchMock).toHaveBeenCalledWith('https://gateway.test/auth/me', {
      headers: { Authorization: 'Bearer refreshed' },
    });
  });
});
