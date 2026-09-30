import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const authMocks = vi.hoisted(() => ({
  sessionLoad: vi.fn(),
  sessionRefresh: vi.fn(),
  sessionIsExpired: vi.fn(),
  credentialsLoad: vi.fn(),
  credentialsRefresh: vi.fn(),
  credentialsIsExpired: vi.fn(),
}));

vi.mock('@kb-labs/sdk', () => ({ useEnv: (name: string) => process.env[name] }));
vi.mock('@kb-labs/cli-runtime/gateway', () => ({
  SessionManager: vi.fn(function SessionManagerMock() {
    return {
      load: authMocks.sessionLoad,
      refresh: authMocks.sessionRefresh,
      isExpired: authMocks.sessionIsExpired,
    };
  }),
  CredentialsManager: vi.fn(function CredentialsManagerMock() {
    return {
      load: authMocks.credentialsLoad,
      refresh: authMocks.credentialsRefresh,
      isExpired: authMocks.credentialsIsExpired,
    };
  }),
}));

import { WorkflowDaemonClient } from '../http-client.js';

describe('WorkflowDaemonClient gateway authentication', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn());
    authMocks.sessionLoad.mockResolvedValue(null);
    authMocks.credentialsLoad.mockResolvedValue(null);
    authMocks.sessionIsExpired.mockReturnValue(false);
    authMocks.credentialsIsExpired.mockReturnValue(false);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('forwards a logged-in session through the project route', async () => {
    authMocks.sessionLoad.mockResolvedValue({
      gatewayUrl: 'http://gateway.test',
      accessToken: 'session-token',
      refreshToken: 'session-refresh',
      expiresAt: Date.now() + 60_000,
    });
    vi.mocked(fetch).mockResolvedValue(new Response(
      JSON.stringify({ ok: true, service: 'workflow' }),
      { status: 200, headers: { 'content-type': 'application/json' } },
    ));

    await new WorkflowDaemonClient({ projectId: 'prj_0123456789abcdef' }).health();

    const [url, init] = vi.mocked(fetch).mock.calls[0] ?? [];
    expect(url).toBe('http://gateway.test/api/v1/projects/prj_0123456789abcdef/workflow/health');
    expect(new Headers(init?.headers).get('authorization')).toBe('Bearer session-token');
  });

  it('refreshes an expired gateway session once after a 401', async () => {
    const expired = {
      gatewayUrl: 'http://gateway.test',
      accessToken: 'expired-token',
      refreshToken: 'session-refresh',
      expiresAt: Date.now() - 1,
    };
    const refreshed = { ...expired, accessToken: 'fresh-token', expiresAt: Date.now() + 60_000 };
    authMocks.sessionLoad.mockResolvedValue(expired);
    authMocks.sessionRefresh.mockResolvedValue(refreshed);
    authMocks.sessionIsExpired.mockReturnValue(true);
    vi.mocked(fetch)
      .mockResolvedValueOnce(new Response('unauthorized', { status: 401 }))
      .mockResolvedValueOnce(new Response(
        JSON.stringify({ ok: true, service: 'workflow' }),
        { status: 200, headers: { 'content-type': 'application/json' } },
      ));

    await new WorkflowDaemonClient({ projectId: 'prj_0123456789abcdef' }).health();

    expect(authMocks.sessionRefresh).toHaveBeenCalledTimes(2);
    const [url, init] = vi.mocked(fetch).mock.calls[1] ?? [];
    expect(url).toBe('http://gateway.test/api/v1/projects/prj_0123456789abcdef/workflow/health');
    expect(new Headers(init?.headers).get('authorization')).toBe('Bearer fresh-token');
  });
});
