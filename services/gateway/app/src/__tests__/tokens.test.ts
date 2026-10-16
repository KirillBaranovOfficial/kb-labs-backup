import { describe, it, expect, vi } from 'vitest';
import { authRejectionEnvelope, resolveToken, extractBearerToken } from '../auth/tokens.js';
import { signAccessToken } from '@kb-labs/gateway-auth';
import type { ICache } from '@kb-labs/core-platform';
import type { JwtConfig } from '@kb-labs/gateway-auth';

function makeCache(entries: Record<string, unknown> = {}): ICache {
  return {
    get: vi.fn(async (key: string) => entries[key] ?? null),
    set: vi.fn(),
    delete: vi.fn(),
    clear: vi.fn(),
  } as unknown as ICache;
}

describe('authRejectionEnvelope', () => {
  // case: S-203.20
  it('uses catalogued actionable codes and fills the actual Gateway URL', () => {
    const gatewayUrl = 'https://gateway.example.test:444';
    const expectedCodes = [
      ['expired', 'KB_AUTH_TOKEN_EXPIRED'],
      ['reissue-required', 'KB_AUTH_TOKEN_REISSUE_REQUIRED'],
      ['revoked', 'KB_AUTH_SESSION_REVOKED'],
      ['inactive', 'KB_AUTH_USER_INACTIVE'],
      ['invalid', 'KB_AUTH_REQUIRED'],
      ['unknown-issuer', 'KB_AUTH_REQUIRED'],
    ] as const;
    for (const [reason, code] of expectedCodes) {
      const envelope = authRejectionEnvelope(reason, gatewayUrl, 'request-1');
      expect(envelope).toMatchObject({
        code,
        details: { gatewayUrl, reason },
        correlationId: 'request-1',
      });
      expect(envelope.hint).toContain(gatewayUrl);
    }
    expect(authRejectionEnvelope(undefined, gatewayUrl, 'request-2')).toMatchObject({
      code: 'KB_AUTH_REQUIRED', details: { gatewayUrl }, correlationId: 'request-2',
    });
  });
});

describe('extractBearerToken', () => {
  it('extracts token from Bearer header', () => {
    expect(extractBearerToken('Bearer abc-123')).toBe('abc-123');
  });

  it('is case-insensitive', () => {
    expect(extractBearerToken('bearer abc-123')).toBe('abc-123');
    expect(extractBearerToken('BEARER abc-123')).toBe('abc-123');
  });

  it('returns null for missing header', () => {
    expect(extractBearerToken(undefined)).toBeNull();
  });

  it('returns null for non-Bearer scheme', () => {
    expect(extractBearerToken('Basic dXNlcjpwYXNz')).toBeNull();
  });

  it('returns null for empty string', () => {
    expect(extractBearerToken('')).toBeNull();
  });

  it('handles token with special characters', () => {
    expect(extractBearerToken('Bearer 8d006616-9c5e-466f-a72f-1c6a6dc20a60')).toBe(
      '8d006616-9c5e-466f-a72f-1c6a6dc20a60',
    );
  });
});

const stubJwtConfig: JwtConfig = { secret: 'test-secret' };

describe('resolveToken', () => {
  it('resolves a valid machine JWT and returns the embedded permissions', async () => {
    const { token } = await signAccessToken(
      {
        hostId: 'host-1',
        namespaceId: 'ns-1',
        tier: 'free',
        type: 'machine_access',
        permissions: ['host:connect'],
      },
      stubJwtConfig,
    );

    const cache = makeCache();
    const ctx = await resolveToken(token, cache, stubJwtConfig);

    expect(ctx.ok).toBe(true);
    if (!ctx.ok) { throw new Error(`Unexpected token rejection: ${ctx.reason}`); }
    expect(ctx.payload.type).toBe('machine');
    expect(ctx.payload.userId).toBe('host-1');
    expect(ctx.payload.namespaceId).toBe('ns-1');
    expect(ctx.payload.permissions).toContain('host:connect');
  });

  it('resolves host-registry opaque token from cache (issued by /hosts/register)', async () => {
    const token = 'host-registry-uuid';
    const cache = makeCache({
      [`host:token:${token}`]: { hostId: 'host-1', namespaceId: 'ns-1' },
    });

    const ctx = await resolveToken(token, cache, stubJwtConfig);

    expect(ctx.ok).toBe(true);
    if (!ctx.ok) { throw new Error(`Unexpected token rejection: ${ctx.reason}`); }
    expect(ctx.payload.type).toBe('machine');
    expect(ctx.payload.userId).toBe('host-1');
    expect(ctx.payload.namespaceId).toBe('ns-1');
    expect(ctx.payload.permissions).toContain('host:connect');
  });

  it('returns a typed invalid category for an unknown token', async () => {
    const cache = makeCache();
    const ctx = await resolveToken('not-a-valid-token', cache, stubJwtConfig);
    expect(ctx).toEqual({ ok: false, reason: 'invalid' });
  });

  it('returns null for a JWT signed with a different secret', async () => {
    const { token } = await signAccessToken(
      { hostId: 'h', namespaceId: 'ns', tier: 'free', type: 'machine_access', permissions: [] },
      { secret: 'other-secret' },
    );

    const cache = makeCache();
    const ctx = await resolveToken(token, cache, stubJwtConfig);
    expect(ctx).toEqual({ ok: false, reason: 'invalid' });
  });

  it('embeds custom permissions in JWT path', async () => {
    const { token } = await signAccessToken(
      {
        hostId: 'h',
        namespaceId: 'ns',
        tier: 'free',
        type: 'machine_access',
        permissions: ['machine:register', 'host:connect'],
      },
      stubJwtConfig,
    );

    const cache = makeCache();
    const ctx = await resolveToken(token, cache, stubJwtConfig);
    expect(ctx.ok).toBe(true);
    if (!ctx.ok) { throw new Error(`Unexpected token rejection: ${ctx.reason}`); }
    expect(ctx.payload.permissions).toContain('machine:register');
    expect(ctx.payload.permissions).toContain('host:connect');
  });
});
