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
    expect(payload).not.toBeNull();
    expect(payload!.sub).toBe('host_abc');
    expect(payload!.namespaceId).toBe('ns1');
    expect(payload!.tier).toBe('free');
    expect(payload!.type).toBe('machine_access');
  });

  it('returns null for tampered token', async () => {
    const { token } = await signAccessToken(
      { hostId: 'host_abc', namespaceId: 'ns1', tier: 'free', type: 'machine_access' },
      config,
    );
    const tampered = token.slice(0, -5) + 'XXXXX';
    const result = await verifyAccessToken(tampered, config);
    expect(result).toBeNull();
  });

  it('returns null for wrong secret', async () => {
    const { token } = await signAccessToken(
      { hostId: 'host_abc', namespaceId: 'ns1', tier: 'free', type: 'machine_access' },
      config,
    );
    const result = await verifyAccessToken(token, { secret: 'wrong-secret-at-least-32-chars!!' });
    expect(result).toBeNull();
  });

  it('rejects a user access token by its explicit kind', async () => {
    const now = Math.floor(Date.now() / 1000);
    const token = await new SignJWT({ namespaceId: 'ns1', tier: 'free', type: 'user_access', permissions: [] })
      .setProtectedHeader({ alg: 'HS256' }).setSubject('user-1').setIssuedAt(now)
      .setExpirationTime(now + 900).sign(new TextEncoder().encode(config.secret));
    await expect(verifyAccessToken(token, config)).resolves.toBeNull();
  });
});

describe('signRefreshToken / verifyRefreshToken', () => {
  it('issues a verifiable refresh token', async () => {
    const token = await signRefreshToken('host_abc', config);
    expect(token).toBeTruthy();

    const result = await verifyRefreshToken(token, config);
    expect(result).not.toBeNull();
    expect(result!.hostId).toBe('host_abc');
  });

  it('returns null for access token passed as refresh', async () => {
    const { token } = await signAccessToken(
      { hostId: 'host_abc', namespaceId: 'ns1', tier: 'free', type: 'machine_access' },
      config,
    );
    // access token has type: 'machine_access', not 'machine_refresh'
    const result = await verifyRefreshToken(token, config);
    expect(result).toBeNull();
  });

  it('returns null for wrong secret', async () => {
    const token = await signRefreshToken('host_abc', config);
    const result = await verifyRefreshToken(token, { secret: 'wrong-secret-at-least-32-chars!!' });
    expect(result).toBeNull();
  });

  it('rejects refresh tokens with claims outside the machine contract', async () => {
    const now = Math.floor(Date.now() / 1000);
    const token = await new SignJWT({ type: 'machine_refresh', jti: 'machine-jti', extra: true })
      .setProtectedHeader({ alg: 'HS256' })
      .setSubject('host_abc')
      .setIssuedAt(now)
      .setExpirationTime(now + 3600)
      .sign(new TextEncoder().encode(config.secret));

    await expect(verifyRefreshToken(token, config)).resolves.toBeNull();
  });

  it('rejects a user refresh token with claims outside the user contract', async () => {
    const now = Math.floor(Date.now() / 1000);
    const token = await new SignJWT({
      type: 'user_refresh', jti: 'user-jti', tenantId: 'tenant-1', fam: 'family-1', extra: true,
    })
      .setProtectedHeader({ alg: 'HS256' })
      .setSubject('user-1')
      .setIssuedAt(now)
      .setExpirationTime(now + 3600)
      .sign(new TextEncoder().encode(config.secret));

    await expect(verifyUserRefreshToken(token, config)).resolves.toBeNull();
  });

  it('verifies machine refresh tokens by explicit kind', async () => {
    const token = await signRefreshToken('host_legacy', config);
    await expect(verifyRefreshToken(token, config)).resolves.toEqual({ hostId: 'host_legacy' });
  });

  it('validates and classifies user refresh tokens by explicit kind', async () => {
    const token = await signUserRefreshToken({
      userId: 'user-1', tenantId: 'tenant-1', familyId: 'family-1', ttlSec: 3600,
    }, config);
    await expect(verifyUserRefreshToken(token, config)).resolves.toMatchObject({
      userId: 'user-1', tenantId: 'tenant-1', familyId: 'family-1', type: 'user_refresh',
    });
    await expect(verifyRefreshTokenKind(token, config)).resolves.toMatchObject({ kind: 'user' });
    await expect(verifyRefreshToken(token, config)).resolves.toBeNull();
  });

  it('rejects a pre-contract refresh token without an explicit kind', async () => {
    const now = Math.floor(Date.now() / 1000);
    const token = await new SignJWT({ type: 'refresh', jti: 'old-jti' })
      .setProtectedHeader({ alg: 'HS256' }).setSubject('host_old').setIssuedAt(now)
      .setExpirationTime(now + 3600).sign(new TextEncoder().encode(config.secret));
    await expect(verifyRefreshTokenKind(token, config)).resolves.toBeNull();
  });
});
