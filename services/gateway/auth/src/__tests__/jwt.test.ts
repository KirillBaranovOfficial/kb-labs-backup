import { describe, it, expect } from 'vitest';
import { SignJWT } from 'jose';
import {
  signAccessToken,
  signRefreshToken,
  signUserRefreshToken,
  verifyAccessToken,
  verifyRefreshToken,
  verifyUserRefreshToken,
  verifyRefreshTokenKind,
  KB_JWT_ISSUER,
} from '../jwt.js';

const config = { secret: 'test-secret-at-least-32-chars-long!!' };

describe('signAccessToken / verifyAccessToken', () => {
  it('issues a verifiable access token', async () => {
    const { token, expiresIn } = await signAccessToken(
      { hostId: 'host_abc', namespaceId: 'ns1', tier: 'free', type: 'machine_access' },
      config,
    );

    expect(token).toBeTruthy();
    expect(expiresIn).toBe(15 * 60);

    const payload = await verifyAccessToken(token, config);
    expect(payload.ok).toBe(true);
    if (!payload.ok) { throw new Error(`Unexpected verification failure: ${payload.reason}`); }
    expect(payload.payload.sub).toBe('host_abc');
    expect(payload.payload.namespaceId).toBe('ns1');
    expect(payload.payload.tier).toBe('free');
    expect(payload.payload.type).toBe('machine_access');
  });

  // case: S-259.1
  it('classifies a validly signed machine access token that misses a current required claim for reissue', async () => {
    const now = Math.floor(Date.now() / 1000);
    const token = await new SignJWT({ namespaceId: 'ns1', tier: 'free', type: 'machine_access', permissions: [] })
      .setProtectedHeader({ alg: 'HS256' }).setIssuer(KB_JWT_ISSUER).setSubject('host-1')
      .setIssuedAt(now).setExpirationTime(now + 900).sign(new TextEncoder().encode(config.secret));
    await expect(verifyAccessToken(token, config)).resolves.toEqual({ ok: false, reason: 'reissue-required' });
  });

  // case: S-203.20
  it('classifies an unknown issuer for machine access and refresh tokens', async () => {
    const now = Math.floor(Date.now() / 1000);
    const key = new TextEncoder().encode(config.secret);
    const access = await new SignJWT({ namespaceId: 'ns1', tier: 'free', type: 'machine_access', permissions: [] })
      .setProtectedHeader({ alg: 'HS256' }).setIssuer('unknown').setSubject('host-1')
      .setIssuedAt(now).setExpirationTime(now + 900).sign(key);
    const refresh = await new SignJWT({ type: 'machine_refresh', jti: 'refresh-1' })
      .setProtectedHeader({ alg: 'HS256' }).setIssuer('unknown').setSubject('host-1')
      .setIssuedAt(now).setExpirationTime(now + 900).sign(key);

    await expect(verifyAccessToken(access, config)).resolves.toEqual({ ok: false, reason: 'unknown-issuer' });
    await expect(verifyRefreshTokenKind(refresh, config)).resolves.toEqual({ ok: false, reason: 'unknown-issuer' });
  });

  // case: S-203.20
  it('classifies a KB-shaped token without an issuer as unknown issuer', async () => {
    const now = Math.floor(Date.now() / 1000);
    const token = await new SignJWT({ namespaceId: 'ns1', tier: 'free', type: 'machine_access', permissions: [] })
      .setProtectedHeader({ alg: 'HS256' }).setSubject('host-1').setIssuedAt(now)
      .setExpirationTime(now + 900).sign(new TextEncoder().encode(config.secret));
    await expect(verifyAccessToken(token, config)).resolves.toEqual({ ok: false, reason: 'unknown-issuer' });
  });

  // case: S-203.20
  it('returns the typed invalid reason for a tampered token', async () => {
    const { token } = await signAccessToken(
      { hostId: 'host_abc', namespaceId: 'ns1', tier: 'free', type: 'machine_access' },
      config,
    );
    const tampered = token.slice(0, -5) + 'XXXXX';
    const result = await verifyAccessToken(tampered, config);
    expect(result).toEqual({ ok: false, reason: 'invalid' });
  });

  it('returns null for wrong secret', async () => {
    const { token } = await signAccessToken(
      { hostId: 'host_abc', namespaceId: 'ns1', tier: 'free', type: 'machine_access' },
      config,
    );
    const result = await verifyAccessToken(token, { secret: 'wrong-secret-at-least-32-chars!!' });
    expect(result).toEqual({ ok: false, reason: 'invalid' });
  });

  it('rejects a user access token by its explicit kind', async () => {
    const now = Math.floor(Date.now() / 1000);
    const token = await new SignJWT({ namespaceId: 'ns1', tier: 'free', type: 'user_access', permissions: [] })
      .setProtectedHeader({ alg: 'HS256' }).setIssuer(KB_JWT_ISSUER).setSubject('user-1').setIssuedAt(now)
      .setExpirationTime(now + 900).sign(new TextEncoder().encode(config.secret));
    await expect(verifyAccessToken(token, config)).resolves.toMatchObject({ ok: false, reason: 'invalid' });
  });
});

describe('signRefreshToken / verifyRefreshToken', () => {
  it('issues a verifiable refresh token', async () => {
    const token = await signRefreshToken('host_abc', config);
    expect(token).toBeTruthy();

    const result = await verifyRefreshToken(token, config);
    expect(result).toEqual({ ok: true, payload: { hostId: 'host_abc' } });
  });

  it('returns null for access token passed as refresh', async () => {
    const { token } = await signAccessToken(
      { hostId: 'host_abc', namespaceId: 'ns1', tier: 'free', type: 'machine_access' },
      config,
    );
    // access token has type: 'machine_access', not 'machine_refresh'
    const result = await verifyRefreshToken(token, config);
    expect(result).toEqual({ ok: false, reason: 'invalid' });
  });

  it('returns null for wrong secret', async () => {
    const token = await signRefreshToken('host_abc', config);
    const result = await verifyRefreshToken(token, { secret: 'wrong-secret-at-least-32-chars!!' });
    expect(result).toEqual({ ok: false, reason: 'invalid' });
  });

  it('rejects refresh tokens with claims outside the machine contract', async () => {
    const now = Math.floor(Date.now() / 1000);
    const token = await new SignJWT({ type: 'machine_refresh', jti: 'machine-jti', extra: true })
      .setProtectedHeader({ alg: 'HS256' })
      .setIssuer(KB_JWT_ISSUER)
      .setSubject('host_abc')
      .setIssuedAt(now)
      .setExpirationTime(now + 3600)
      .sign(new TextEncoder().encode(config.secret));

    await expect(verifyRefreshToken(token, config)).resolves.toMatchObject({ ok: false, reason: 'invalid' });
  });

  it('rejects a user refresh token with claims outside the user contract', async () => {
    const now = Math.floor(Date.now() / 1000);
    const token = await new SignJWT({
      type: 'user_refresh', jti: 'user-jti', tenantId: 'tenant-1', fam: 'family-1', extra: true,
    })
      .setProtectedHeader({ alg: 'HS256' })
      .setIssuer(KB_JWT_ISSUER)
      .setSubject('user-1')
      .setIssuedAt(now)
      .setExpirationTime(now + 3600)
      .sign(new TextEncoder().encode(config.secret));

    await expect(verifyUserRefreshToken(token, config)).resolves.toMatchObject({ ok: false, reason: 'invalid' });
  });

  it('verifies machine refresh tokens by explicit kind', async () => {
    const token = await signRefreshToken('host_legacy', config);
    await expect(verifyRefreshToken(token, config)).resolves.toEqual({ ok: true, payload: { hostId: 'host_legacy' } });
  });

  it('validates and classifies user refresh tokens by explicit kind', async () => {
    const token = await signUserRefreshToken({
      userId: 'user-1', tenantId: 'tenant-1', familyId: 'family-1', ttlSec: 3600,
    }, config);
    await expect(verifyUserRefreshToken(token, config)).resolves.toMatchObject({
      ok: true, payload: { userId: 'user-1', tenantId: 'tenant-1', familyId: 'family-1', type: 'user_refresh' },
    });
    await expect(verifyRefreshTokenKind(token, config)).resolves.toMatchObject({ ok: true, payload: { kind: 'user' } });
    await expect(verifyRefreshToken(token, config)).resolves.toMatchObject({ ok: false, reason: 'invalid' });
  });

  it('rejects a pre-contract refresh token without an explicit kind', async () => {
    const now = Math.floor(Date.now() / 1000);
    const token = await new SignJWT({ type: 'refresh', jti: 'old-jti' })
      .setProtectedHeader({ alg: 'HS256' }).setSubject('host_old').setIssuedAt(now)
      .setExpirationTime(now + 3600).sign(new TextEncoder().encode(config.secret));
    await expect(verifyRefreshTokenKind(token, config)).resolves.toMatchObject({ ok: false, reason: 'unknown-issuer' });
  });
});
