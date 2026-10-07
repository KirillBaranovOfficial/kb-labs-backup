import { afterEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  session: null as null | { gatewayUrl: string; accessToken: string; refreshToken: string; expiresAt: number },
  machine: null as null | { gatewayUrl: string; accessToken: string; refreshToken: string; expiresAt: number },
  loadError: null as unknown,
  refresh: vi.fn(),
  machineRefresh: vi.fn(),
}));
vi.mock('./session.js', () => ({
  SessionManager: class {
    async load() { if (mocks.loadError) throw mocks.loadError; return mocks.session; }
    isExpired(session: { expiresAt: number }) { return session.expiresAt <= Date.now(); }
    async refresh() { return mocks.refresh(); }
  },
}));
vi.mock('./credentials.js', () => ({
  CredentialsManager: class {
    async load() { return mocks.machine; }
    isExpired(credentials: { expiresAt: number }) { return credentials.expiresAt <= Date.now(); }
    async refresh() { return mocks.machineRefresh(); }
  },
}));

import { gatewayApiFetch } from './api-client.js';
import { readGatewaySuccessData } from './result.js';
import { SavedAuthFileError } from './saved-auth-file.js';
import { createErrorEnvelope, ErrorEnvelopeError } from '@kb-labs/core-platform';

describe('gatewayApiFetch auth mapping', () => {
  afterEach(() => {
    vi.restoreAllMocks(); vi.unstubAllGlobals();
    mocks.session = null; mocks.machine = null; mocks.loadError = null;
    mocks.refresh.mockReset(); mocks.machineRefresh.mockReset();
  });

  // case: S-041.18
  it('preserves a corrupt saved session as a catalogued config error', async () => {
    mocks.loadError = new SavedAuthFileError('.kb/session.json', 'invalid');
    await expect(gatewayApiFetch('http://127.0.0.1:4000/api')).rejects.toMatchObject({
      envelope: {
        code: 'KB_CONFIG_INVALID',
        details: { path: '.kb/session.json', reason: 'invalid' },
        hint: 'Run "kb auth login" to replace the saved authentication data, then retry the command.',
      },
    });
  });

  // case: S-041.7
  it('maps a 401 to the catalog required-auth envelope', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('', { status: 401 })));
    await expect(gatewayApiFetch('http://127.0.0.1:4000/api')).rejects.toMatchObject({
      envelope: {
        code: 'KB_AUTH_REQUIRED',
        hint: 'Run "kb auth login --gateway-url http://127.0.0.1:4000 --email <email>" and enter your password at the prompt, or provide it with --password-stdin.',
        details: { gatewayUrl: 'http://127.0.0.1:4000' },
      },
    });
  });

  it('refreshes a session only at its issuing Gateway origin', async () => {
    const session = { gatewayUrl: 'https://gateway-a.test', accessToken: 'stale', refreshToken: 'refresh', expiresAt: 0 };
    mocks.session = session;
    mocks.refresh.mockResolvedValue({ ...session, accessToken: 'fresh', expiresAt: Date.now() + 60_000 });
    const fetch = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
    vi.stubGlobal('fetch', fetch);

    await gatewayApiFetch('https://gateway-a.test/api', {}, { gatewayUrl: 'https://gateway-a.test' });

    expect(mocks.refresh).toHaveBeenCalledOnce();
    expect(fetch.mock.calls[0]?.[0]).toEqual(new URL('https://gateway-a.test/api'));
    expect(new Headers(fetch.mock.calls[0]?.[1]?.headers).get('Authorization')).toBe('Bearer fresh');
  });

  it('uses an issuing-origin machine credential when no user session is saved', async () => {
    mocks.machine = { gatewayUrl: 'https://gateway-a.test', accessToken: 'machine', refreshToken: 'refresh', expiresAt: Date.now() + 60_000 };
    const fetch = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
    vi.stubGlobal('fetch', fetch);

    await gatewayApiFetch('https://gateway-a.test/api', {}, { gatewayUrl: 'https://gateway-a.test' });

    expect(fetch.mock.calls[0]?.[0]).toEqual(new URL('https://gateway-a.test/api'));
    expect(new Headers(fetch.mock.calls[0]?.[1]?.headers).get('Authorization')).toBe('Bearer machine');
  });

  it('retargets the full request origin to the resolved Gateway when supplied', async () => {
    const fetch = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
    vi.stubGlobal('fetch', fetch);

    await gatewayApiFetch('https://old-gateway.test:4444/api', {}, { gatewayUrl: 'https://active-gateway.test:8443' });

    expect(fetch.mock.calls[0]?.[0]).toEqual(new URL('https://active-gateway.test:8443/api'));
  });

  // case: S-041.9
  it('maps the permission reported by a 403 response without inventing a role', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({
      error: 'Forbidden', message: 'Permission denied: users:write', permission: 'users:write',
    }), { status: 403, headers: { 'Content-Type': 'application/json' } })));
    await expect(gatewayApiFetch('http://127.0.0.1:4000/api')).rejects.toMatchObject({
      envelope: {
        code: 'KB_AUTH_FORBIDDEN',
        details: { permission: 'users:write' },
        message: 'Permission denied: users:write',
      },
    });
  });

  // case: S-203.14
  it('maps a refused host connection to the catalogued host-unreachable envelope', async () => {
    const cause = Object.assign(new Error('connection refused'), { code: 'ECONNREFUSED' });
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('fetch failed', { cause })));
    await expect(gatewayApiFetch('http://127.0.0.1:43123/api')).rejects.toMatchObject({
      envelope: {
        code: 'KB_HOST_UNREACHABLE',
        message: 'The KB Labs host is not running: cannot connect to http://127.0.0.1:43123.',
        hint: 'Run "kb-create start", or "kb-create doctor" if the host should already be running.',
        correlationId: expect.any(String),
      },
    });
  });

  // case: S-203.23
  it('does not send or refresh saved credentials at a different Gateway origin', async () => {
    for (const credentialKind of ['user session', 'machine credential'] as const) {
      mocks.session = null;
      mocks.machine = null;
      const credential = {
        gatewayUrl: 'https://gateway-root-a.test',
        accessToken: 'root-a-access',
        refreshToken: 'root-a-refresh',
        expiresAt: 0,
      };
      if (credentialKind === 'user session') { mocks.session = credential; }
      else { mocks.machine = credential; }
      const fetch = vi.fn();
      vi.stubGlobal('fetch', fetch);

      await expect(gatewayApiFetch(
        'https://gateway-root-b.test/auth/tokens',
        {},
        { gatewayUrl: 'https://gateway-root-b.test' },
      )).rejects.toMatchObject({
        envelope: {
          code: 'KB_AUTH_REQUIRED',
          details: { gatewayUrl: 'https://gateway-root-b.test' },
          hint: expect.stringContaining('kb auth login --gateway-url https://gateway-root-b.test --email <email>'),
        },
      });

      expect(fetch).not.toHaveBeenCalled();
      expect(mocks.refresh).not.toHaveBeenCalled();
      expect(mocks.machineRefresh).not.toHaveBeenCalled();
    }
  });
});

describe('readGatewaySuccessData', () => {
  // case: S-203.20
  it('propagates catalog envelopes and fills the saved Gateway URL generically', async () => {
    for (const [reason, code] of [
      ['expired', 'KB_AUTH_TOKEN_EXPIRED'],
      ['revoked', 'KB_AUTH_SESSION_REVOKED'],
      ['inactive', 'KB_AUTH_USER_INACTIVE'],
      ['unknown-issuer', 'KB_AUTH_REQUIRED'],
    ] as const) {
      const gatewayUrl = 'https://gateway.test:444';
      const envelope = createErrorEnvelope(code, {
        details: { gatewayUrl, reason },
        correlationId: 'corr-auth',
      });
      const response = new Response(JSON.stringify({ ok: false, error: envelope }), {
        headers: { 'Content-Type': 'application/json' },
      });
      await expect(readGatewaySuccessData(response)).rejects.toMatchObject({
        envelope: { ...envelope, details: { gatewayUrl, reason } },
      });
      await expect(readGatewaySuccessData(new Response(JSON.stringify({ ok: false, error: envelope }))))
        .rejects.toBeInstanceOf(ErrorEnvelopeError);
      expect(envelope.hint).toContain(gatewayUrl);
    }
  });

  // case: S-203.1
  it('unwraps the shared success result returned by Gateway JSON routes', async () => {
    const response = new Response(JSON.stringify({ ok: true, data: { accessToken: 'token' } }), {
      headers: { 'Content-Type': 'application/json' },
    });
    await expect(readGatewaySuccessData<{ accessToken: string }>(response)).resolves.toEqual({ accessToken: 'token' });
  });

  // case: S-203.2
  it('rejects invalid or failed result envelopes with a safe error', async () => {
    const invalid = new Response(JSON.stringify({ ok: true }), { headers: { 'Content-Type': 'application/json' } });
    await expect(readGatewaySuccessData(invalid)).rejects.toThrow('invalid result envelope');
    const failure = new Response(JSON.stringify({
      ok: false,
      error: {
        code: 'KB_RUNTIME_UNEXPECTED', area: 'runtime', stage: 'run', severity: 'error', retryable: false,
        message: 'The operation failed unexpectedly.', hint: 'Copy diagnostics and contact support.', correlationId: 'corr-1',
      },
    }), { headers: { 'Content-Type': 'application/json' } });
    await expect(readGatewaySuccessData(failure)).rejects.toMatchObject({
      envelope: { code: 'KB_RUNTIME_UNEXPECTED' },
    });
  });
});
