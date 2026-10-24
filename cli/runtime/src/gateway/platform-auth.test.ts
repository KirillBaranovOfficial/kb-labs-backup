import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createErrorEnvelope } from '@kb-labs/core-platform';

const mocks = vi.hoisted(() => ({
  session: { gatewayUrl: 'https://gateway.test', accessToken: 'saved', refreshToken: 'refresh', expiresAt: Date.now() + 600_000 },
  load: vi.fn(), credentialsLoad: vi.fn(),
}));

vi.mock('@kb-labs/sdk/gateway', () => ({
  SessionManager: class {
    async load() { return mocks.load(); }
    async save() {}
    async clear() {}
    getFilePath() { return '/tmp/session.json'; }
  },
  CredentialsManager: class {
    async load() { return mocks.credentialsLoad(); }
    async save() {}
    async clear() {}
  },
}));


let hasUsablePlatformAuth: typeof import('./platform-auth.js').hasUsablePlatformAuth;
let SavedAuthFileError: typeof import('@kb-labs/sdk/gateway').SavedAuthFileError;
let AuthSessionOwner: typeof import('@kb-labs/sdk/gateway').AuthSessionOwner;

beforeEach(async () => {
  vi.resetModules();
  mocks.load.mockReset();
  mocks.credentialsLoad.mockReset();
  ({ SavedAuthFileError } = await import('@kb-labs/sdk/gateway'));
  ({ AuthSessionOwner } = await import('@kb-labs/sdk/gateway'));
  ({ hasUsablePlatformAuth } = await import('./platform-auth.js'));
  mocks.credentialsLoad.mockResolvedValue(null);
});

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

describe('platform auth verification cache', () => {
  it('delegates the platform preflight to the auth-session owner', async () => {
    const verify = vi.spyOn(AuthSessionOwner.prototype, 'verify').mockResolvedValue(null);
    const target = { gatewayUrl: 'https://gateway.test', platformRoot: '/platform' };

    await expect(hasUsablePlatformAuth(target)).resolves.toEqual({ authenticated: false });

    expect(verify).toHaveBeenCalledWith(target);
  });

  // case: S-203.23
  it('does not refresh or verify saved credentials against a different Gateway origin', async () => {
    for (const credentialKind of ['user session', 'machine credential'] as const) {
      mocks.load.mockReset().mockResolvedValue(null);
      mocks.credentialsLoad.mockReset().mockResolvedValue(null);
      const credential = { ...mocks.session, gatewayUrl: 'https://gateway-a.test' };
      if (credentialKind === 'user session') { mocks.load.mockResolvedValue(credential); }
      else { mocks.credentialsLoad.mockResolvedValue(credential); }
      const fetchMock = vi.fn();
      vi.stubGlobal('fetch', fetchMock);

      await expect(hasUsablePlatformAuth({ gatewayUrl: 'https://gateway-b.test' })).resolves.toMatchObject({
        authenticated: false,
        error: {
          code: 'KB_AUTH_REQUIRED',
          details: { gatewayUrl: 'https://gateway-b.test' },
          hint: expect.stringContaining('kb auth login --gateway-url https://gateway-b.test --email <email>'),
        },
      });

      expect(fetchMock).not.toHaveBeenCalled();
    }
  });

  it('loads, refreshes, and verifies the session once for the process', async () => {
    mocks.load.mockResolvedValue({ ...mocks.session, expiresAt: 0 });
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ ok: true, data: { accessToken: 'refreshed', refreshToken: 'rotated', expiresIn: 900 } })))
      .mockResolvedValueOnce(new Response('{}', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);

    const results = await Promise.all([hasUsablePlatformAuth(), hasUsablePlatformAuth()]);

    expect(results).toEqual([{ authenticated: true }, { authenticated: true }]);
    expect(mocks.load).toHaveBeenCalledOnce();
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(fetchMock).toHaveBeenLastCalledWith('https://gateway.test/auth/me', {
      headers: { Authorization: 'Bearer refreshed' },
    });
  });

  it('preserves an unreachable Gateway as a host error instead of treating it as a rejected token', async () => {
    mocks.load.mockResolvedValue({ ...mocks.session, expiresAt: Date.now() + 600_000 });
    const cause = Object.assign(new Error('connection refused'), { code: 'ECONNREFUSED' });
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('fetch failed', { cause })));

    await expect(hasUsablePlatformAuth()).resolves.toMatchObject({
      authenticated: false,
      error: { code: 'KB_HOST_UNREACHABLE', details: { address: 'https://gateway.test' } },
    });
  });

  it('treats only an HTTP 401 as a rejected saved token', async () => {
    mocks.load.mockResolvedValue({ ...mocks.session, expiresAt: Date.now() + 600_000 });
    const required = createErrorEnvelope('KB_AUTH_REQUIRED', { details: { gatewayUrl: 'https://gateway.test' } });
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ ok: false, error: required }), { status: 401 })));

    await expect(hasUsablePlatformAuth()).resolves.toMatchObject({
      authenticated: false,
      error: { code: 'KB_AUTH_REQUIRED', details: { gatewayUrl: 'https://gateway.test' } },
    });
  });

  // case: S-203.20
  it('preserves a catalogued rejection returned by /auth/me and fills the saved Gateway URL', async () => {
    mocks.load.mockResolvedValue({ ...mocks.session, expiresAt: Date.now() + 600_000 });
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
        hint: 'Run "kb auth login --gateway-url https://gateway.test --email <email>" and enter your password at the prompt, or provide it with --password-stdin.',
      },
    });
  });

  it('treats an HTTP 401 from the refresh endpoint as a rejected saved token', async () => {
    mocks.load.mockResolvedValue({ ...mocks.session, expiresAt: 0 });
    const required = createErrorEnvelope('KB_AUTH_REQUIRED', { details: { gatewayUrl: 'https://gateway.test' } });
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ ok: false, error: required }), { status: 401 })));

    await expect(hasUsablePlatformAuth()).resolves.toMatchObject({
      authenticated: false,
      error: { code: 'KB_AUTH_REQUIRED', details: { gatewayUrl: 'https://gateway.test' } },
    });
  });

  // case: S-203.20
  it('preserves safe auth rejection reasons with a catalog login action', async () => {
    for (const reason of ['expired', 'revoked', 'unknown-issuer'] as const) {
      vi.resetModules();
      ({ hasUsablePlatformAuth } = await import('./platform-auth.js'));
      mocks.load.mockResolvedValue({ ...mocks.session, expiresAt: 0 });
      const code = reason === 'expired'
        ? 'KB_AUTH_TOKEN_EXPIRED'
        : reason === 'revoked' ? 'KB_AUTH_SESSION_REVOKED' : 'KB_AUTH_REQUIRED';
      vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({
        ok: false,
        error: {
          ...createErrorEnvelope(code, { details: { gatewayUrl: '<url>', reason }, correlationId: 'refresh-correlation' }),
        },
      }), { status: 401 })));

      await expect(hasUsablePlatformAuth()).resolves.toMatchObject({
        authenticated: false,
        error: {
          code,
          details: { gatewayUrl: 'https://gateway.test', reason },
          correlationId: 'refresh-correlation',
          hint: reason === 'unknown-issuer'
            ? 'Run "kb auth login --gateway-url https://gateway.test --email <email>" and enter your password at the prompt, or provide it with --password-stdin.'
            : 'Run "kb auth login --gateway-url https://gateway.test --email <email>" and enter your password at the prompt, or provide it with --password-stdin.',
        },
      });
    }
  });

  // case: S-203.2
  it('maps a non-envelope Gateway response to a safe protocol failure with status and origin', async () => {
    mocks.load.mockResolvedValue({ ...mocks.session, expiresAt: Date.now() + 600_000 });
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{}', { status: 503 })));

    await expect(hasUsablePlatformAuth()).resolves.toMatchObject({
      authenticated: false,
      error: {
        code: 'KB_RUNTIME_GATEWAY_PROTOCOL_FAILURE',
        message: 'The Gateway returned an invalid UX result.',
        hint: 'Run "kb-create doctor" to check the Gateway, then retry the command.',
        details: { httpStatus: '503', requestStage: 'Gateway verification', gatewayUrl: 'https://gateway.test' },
        correlationId: expect.any(String),
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
