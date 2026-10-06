import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createErrorEnvelope } from '@kb-labs/core-platform';

const mocks = vi.hoisted(() => ({
  session: { gatewayUrl: 'https://gateway.test', accessToken: 'saved', refreshToken: 'refresh', expiresAt: 0 },
  load: vi.fn(), refresh: vi.fn(), isExpired: vi.fn(() => true),
  credentialsLoad: vi.fn(),
}));

vi.mock('./session.js', () => ({
  SessionManager: class {
    async load() { return mocks.load(); }
    isExpired() { return mocks.isExpired(); }
    async refresh() { return mocks.refresh(); }
  },
}));
vi.mock('./credentials.js', () => ({ CredentialsManager: class { async load() { return mocks.credentialsLoad(); } } }));

let hasUsablePlatformAuth: typeof import('./platform-auth.js').hasUsablePlatformAuth;
let GatewayAuthResponseError: typeof import('./result.js').GatewayAuthResponseError;
let SavedAuthFileError: typeof import('./saved-auth-file.js').SavedAuthFileError;

beforeEach(async () => {
  vi.resetModules();
  mocks.load.mockReset();
  mocks.refresh.mockReset();
  mocks.credentialsLoad.mockReset();
  mocks.isExpired.mockReturnValue(true);
  ({ GatewayAuthResponseError } = await import('./result.js'));
  ({ SavedAuthFileError } = await import('./saved-auth-file.js'));
  ({ hasUsablePlatformAuth } = await import('./platform-auth.js'));
  mocks.credentialsLoad.mockResolvedValue(null);
});

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

describe('platform auth verification cache', () => {
  it('loads, refreshes, and verifies the session once for the process', async () => {
    mocks.load.mockResolvedValue(mocks.session);
    mocks.refresh.mockResolvedValue({ ...mocks.session, accessToken: 'refreshed' });
    const fetchMock = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);

    const results = await Promise.all([hasUsablePlatformAuth(), hasUsablePlatformAuth()]);

    expect(results).toEqual([{ authenticated: true }, { authenticated: true }]);
    expect(mocks.load).toHaveBeenCalledOnce();
    expect(mocks.refresh).toHaveBeenCalledOnce();
    expect(fetchMock).toHaveBeenCalledOnce();
    expect(fetchMock).toHaveBeenCalledWith('https://gateway.test/auth/me', {
      headers: { Authorization: 'Bearer refreshed' },
    });
  });

  it('preserves an unreachable Gateway as a host error instead of treating it as a rejected token', async () => {
    mocks.load.mockResolvedValue({ ...mocks.session, expiresAt: Date.now() + 600_000 });
    mocks.isExpired.mockReturnValue(false);
    const cause = Object.assign(new Error('connection refused'), { code: 'ECONNREFUSED' });
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('fetch failed', { cause })));

    await expect(hasUsablePlatformAuth()).resolves.toMatchObject({
      authenticated: false,
      error: { code: 'KB_HOST_UNREACHABLE', details: { address: 'https://gateway.test' } },
    });
  });

  it('treats only an HTTP 401 as a rejected saved token', async () => {
    mocks.load.mockResolvedValue({ ...mocks.session, expiresAt: Date.now() + 600_000 });
    mocks.isExpired.mockReturnValue(false);
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{}', { status: 401 })));

    await expect(hasUsablePlatformAuth()).resolves.toMatchObject({
      authenticated: false,
      error: { code: 'KB_AUTH_REQUIRED', details: { gatewayUrl: 'https://gateway.test' } },
    });
  });

  // case: S-203.20
  it('preserves a catalogued rejection returned by /auth/me and fills the saved Gateway URL', async () => {
    mocks.load.mockResolvedValue({ ...mocks.session, expiresAt: Date.now() + 600_000 });
    mocks.isExpired.mockReturnValue(false);
    const envelope = createErrorEnvelope('KB_AUTH_SESSION_REVOKED', {
      details: { gatewayUrl: '<url>', reason: 'revoked' }, correlationId: 'me-correlation',
    });
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ ok: false, error: envelope }), {
      status: 401, headers: { 'Content-Type': 'application/json' },
    })));

    await expect(hasUsablePlatformAuth()).resolves.toMatchObject({
      authenticated: false,
      error: {
        code: 'KB_AUTH_SESSION_REVOKED',
        message: 'The saved Gateway session was revoked.',
        details: { gatewayUrl: 'https://gateway.test', reason: 'revoked' },
        correlationId: 'me-correlation',
        hint: 'Run "kb auth login --gateway-url https://gateway.test --email <email> --password <password>" to start a new session.',
      },
    });
  });

  it('treats an HTTP 401 from the refresh endpoint as a rejected saved token', async () => {
    mocks.load.mockResolvedValue(mocks.session);
    mocks.refresh.mockRejectedValue(new GatewayAuthResponseError('Session refresh', 401, 'unauthorized'));

    await expect(hasUsablePlatformAuth()).resolves.toMatchObject({
      authenticated: false,
      error: { code: 'KB_AUTH_REQUIRED', details: { gatewayUrl: 'https://gateway.test' } },
    });
  });

  // case: S-203.20
  it('preserves safe auth rejection reasons with a catalog login action', async () => {
    for (const reason of ['expired', 'revoked', 'unknown-issuer'] as const) {
      vi.resetModules();
      ({ GatewayAuthResponseError } = await import('./result.js'));
      ({ hasUsablePlatformAuth } = await import('./platform-auth.js'));
      mocks.load.mockResolvedValue(mocks.session);
      const code = reason === 'expired'
        ? 'KB_AUTH_TOKEN_EXPIRED'
        : reason === 'revoked' ? 'KB_AUTH_SESSION_REVOKED' : 'KB_AUTH_REQUIRED';
      mocks.refresh.mockRejectedValue(new GatewayAuthResponseError('Session refresh', 401, JSON.stringify({
        ok: false,
        error: {
          ...createErrorEnvelope(code, { details: { gatewayUrl: '<url>', reason }, correlationId: 'refresh-correlation' }),
        },
      }), 'https://gateway.test'));

      await expect(hasUsablePlatformAuth()).resolves.toMatchObject({
        authenticated: false,
        error: {
          code,
          details: { gatewayUrl: 'https://gateway.test', reason },
          correlationId: 'refresh-correlation',
          hint: reason === 'revoked'
            ? 'Run "kb auth login --gateway-url https://gateway.test --email <email> --password <password>" to start a new session.'
            : 'Run "kb auth login --gateway-url https://gateway.test --email <email> --password <password>".',
        },
      });
    }
  });

  it('preserves a non-auth Gateway response as a catalogued command failure', async () => {
    mocks.load.mockResolvedValue({ ...mocks.session, expiresAt: Date.now() + 600_000 });
    mocks.isExpired.mockReturnValue(false);
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{}', { status: 503 })));

    await expect(hasUsablePlatformAuth()).resolves.toMatchObject({
      authenticated: false,
      error: {
        code: 'KB_RUNTIME_COMMAND_FAILED',
        message: 'The Gateway could not verify the saved session (HTTP 503).',
        hint: 'Run "kb-create doctor" to check the host, then retry the command.',
      },
    });
  });

  it.each([
    ['session', '.kb/session.json', 'session'],
    ['machine credentials', '.kb/credentials.json', 'credentials'],
  ])('reports a corrupt %s file with a catalogued repair hint', async (_kind, filePath, source) => {
    if (source === 'session') {
      mocks.load.mockRejectedValue(new SavedAuthFileError(filePath, 'invalid'));
    } else {
      mocks.credentialsLoad.mockRejectedValue(new SavedAuthFileError(filePath, 'unreadable'));
    }

    await expect(hasUsablePlatformAuth()).resolves.toMatchObject({
      authenticated: false,
      error: {
        code: 'KB_CONFIG_INVALID',
        details: { path: filePath },
        hint: 'Run "kb auth login" to replace the saved authentication data, then retry the command.',
      },
    });
  });
});
