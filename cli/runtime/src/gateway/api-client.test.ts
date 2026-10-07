import { afterEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({ session: null as null | { gatewayUrl: string; accessToken: string; refreshToken: string; expiresAt: number }, loadError: null as unknown, refresh: vi.fn() }));
vi.mock('./session.js', () => ({
  SessionManager: class {
    async load() { if (mocks.loadError) throw mocks.loadError; return mocks.session; }
    isExpired() { return false; }
    async refresh() { return mocks.refresh(); }
  },
}));
vi.mock('./credentials.js', () => ({
  CredentialsManager: class { async load() { return null; } },
}));

import { gatewayApiFetch } from './api-client.js';
import { readGatewaySuccessData } from './result.js';
import { SavedAuthFileError } from './saved-auth-file.js';
import { createErrorEnvelope, ErrorEnvelopeError } from '@kb-labs/core-platform';

describe('gatewayApiFetch auth mapping', () => {
  afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); mocks.session = null; mocks.loadError = null; mocks.refresh.mockReset(); });

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
