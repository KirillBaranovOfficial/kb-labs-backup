import { describe, it, expect } from 'vitest';
import { SignJWT } from 'jose';
import {
  signAccessToken,
  signRefreshToken,
  signUserRefreshToken,
  verifyAccessToken,
  verifyRefreshToken,
  verifyUserRefreshToken,
} from '../jwt.js';

const config = { secret: 'test-secret-at-least-32-chars-long!!' };

describe('signAccessToken / verifyAccessToken', () => {
  it('issues a verifiable access token', async () => {
    const { token, expiresIn } = await signAccessToken(
      { hostId: 'host_abc', namespaceId: 'ns1', tier: 'free', type: 'machine' },
      config,
    );

    expect(token).toBeTruthy();
    expect(expiresIn).toBe(15 * 60);

    const payload = await verifyAccessToken(token, config);
    expect(payload).not.toBeNull();
    expect(payload!.sub).toBe('host_abc');
    expect(payload!.namespaceId).toBe('ns1');
    expect(payload!.tier).toBe('free');
    expect(payload!.type).toBe('machine');
  });

  it('returns null for tampered token', async () => {
    const { token } = await signAccessToken(
      { hostId: 'host_abc', namespaceId: 'ns1', tier: 'free', type: 'machine' },
      config,
    );
    const tampered = token.slice(0, -5) + 'XXXXX';
    const result = await verifyAccessToken(tampered, config);
    expect(result).toBeNull();
  });

  it('returns null for wrong secret', async () => {
    const { token } = await signAccessToken(
      { hostId: 'host_abc', namespaceId: 'ns1', tier: 'free', type: 'machine' },
      config,
    );
    const result = await verifyAccessToken(token, { secret: 'wrong-secret-at-least-32-chars!!' });
    expect(result).toBeNull();
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
      { hostId: 'host_abc', namespaceId: 'ns1', tier: 'free', type: 'machine' },
      config,
    );
    // access token has type: 'machine', not 'refresh'
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
    const token = await new SignJWT({ type: 'refresh', jti: 'machine-jti', extra: true })
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
      type: 'refresh', jti: 'user-jti', tenantId: 'tenant-1', fam: 'family-1', extra: true,
    })
      .setProtectedHeader({ alg: 'HS256' })
      .setSubject('user-1')
      .setIssuedAt(now)
      .setExpirationTime(now + 3600)
      .sign(new TextEncoder().encode(config.secret));

    await expect(verifyUserRefreshToken(token, config)).resolves.toBeNull();
  });

  it('keeps the previous-release machine refresh token contract verifiable', async () => {
    const token = await signRefreshToken('host_legacy', config);
    await expect(verifyRefreshToken(token, config)).resolves.toEqual({ hostId: 'host_legacy' });
  });

  it('validates and classifies user refresh tokens by their exact contract', async () => {
    const token = await signUserRefreshToken({
      userId: 'user-1', tenantId: 'tenant-1', familyId: 'family-1', ttlSec: 3600,
    }, config);
    await expect(verifyUserRefreshToken(token, config)).resolves.toMatchObject({
      userId: 'user-1', tenantId: 'tenant-1', familyId: 'family-1', type: 'refresh',
    });
  });
});
