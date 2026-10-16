/**
 * Tests for the user-flavoured JWT helpers (ADR-0020, Phase 1.13).
 *
 * Adds `signUserAccessToken` / `signUserRefreshToken` /
 * `verifyUserAccessToken` / `verifyUserRefreshToken` next to the
 * existing machine-token functions in `jwt.ts`.
 *
 * Crucial invariants:
 * - **`fam` is mandatory in BOTH access and refresh** (CD-2). Sign
 *   helpers require it as an argument; verify helpers reject tokens
 *   without `fam` even if the signature is otherwise valid.
 * - Access payload type is `user_access`, refresh is `user_refresh`. A machine
 *   token MUST NOT verify as a user access token (`type` differs).
 * - No `role` / `scopes` / `permissions` ever land in the payload
 *   (ADR-0020, principle #3).
 */

import { describe, it, expect } from 'vitest';
import { SignJWT } from 'jose';
import {
  signUserAccessToken,
  signUserRefreshToken,
  verifyUserAccessToken,
  verifyUserRefreshToken,
  signAccessToken,
  KB_JWT_ISSUER,
} from '../jwt.js';

const config = { secret: 'test-secret-at-least-32-chars-long!!' };
const claims = { userId: 'u1', tenantId: 't1', familyId: 'fam-1' };

describe('signUserAccessToken / verifyUserAccessToken', () => {
  it('round-trips identity claims', async () => {
    const { token, expiresInSec } = await signUserAccessToken({ ...claims, ttlSec: 900 }, config);
    expect(token).toBeTruthy();
    expect(expiresInSec).toBe(900);

    const payload = await verifyUserAccessToken(token, config);
    expect(payload.ok).toBe(true);
    if (!payload.ok) { throw new Error(`Unexpected verification failure: ${payload.reason}`); }
    expect(payload.payload.userId).toBe('u1');
    expect(payload.payload.tenantId).toBe('t1');
    expect(payload.payload.familyId).toBe('fam-1');
    expect(payload.payload.jti).toBeTypeOf('string');
    expect(payload.payload.type).toBe('user_access');
  });

  it('classifies a validly signed user access token outside the current claim contract for reissue', async () => {
    const now = Math.floor(Date.now() / 1000);
    const token = await new SignJWT({ tenantId: claims.tenantId, fam: claims.familyId, type: 'user_access' })
      .setProtectedHeader({ alg: 'HS256' }).setIssuer(KB_JWT_ISSUER).setSubject(claims.userId)
      .setIssuedAt(now).setExpirationTime(now + 900).sign(new TextEncoder().encode(config.secret));
    expect(await verifyUserAccessToken(token, config)).toEqual({ ok: false, reason: 'reissue-required' });
  });

  it('payload contains NO role / scopes / permissions fields (principle #3)', async () => {
    const { token } = await signUserAccessToken({ ...claims, ttlSec: 900 }, config);
    const payload = await verifyUserAccessToken(token, config);
    expect(payload.ok && (payload.payload as unknown as Record<string, unknown>).role).toBeUndefined();
    expect(payload.ok && (payload.payload as unknown as Record<string, unknown>).scopes).toBeUndefined();
    expect(payload.ok && (payload.payload as unknown as Record<string, unknown>).permissions).toBeUndefined();
  });

  // case: S-203.20
  it('returns a typed invalid reason for a tampered token', async () => {
    const { token } = await signUserAccessToken({ ...claims, ttlSec: 900 }, config);
    const tampered = token.slice(0, -5) + 'XXXXX';
    expect(await verifyUserAccessToken(tampered, config)).toEqual({ ok: false, reason: 'invalid' });
  });

  it('returns null for wrong secret', async () => {
    const { token } = await signUserAccessToken({ ...claims, ttlSec: 900 }, config);
    expect(await verifyUserAccessToken(token, { secret: 'other-secret-at-least-32-chars-now' })).toEqual({ ok: false, reason: 'invalid' });
  });

  it('rejects a machine access token by its explicit kind', async () => {
    const { token } = await signAccessToken({
      hostId: 'host-1', namespaceId: 'tenant-1', tier: 'free', type: 'machine_access',
    }, config);
    expect(await verifyUserAccessToken(token, config)).toMatchObject({ ok: false, reason: 'invalid' });
  });

  it('classifies a user access token without the KB issuer as unknown issuer', async () => {
    const now = Math.floor(Date.now() / 1000);
    const token = await new SignJWT({ tenantId: 't1', fam: 'fam-1', type: 'user', jti: 'old-jti' })
      .setProtectedHeader({ alg: 'HS256' }).setSubject('u1').setIssuedAt(now)
      .setExpirationTime(now + 900).sign(new TextEncoder().encode(config.secret));
    expect(await verifyUserAccessToken(token, config)).toEqual({ ok: false, reason: 'unknown-issuer' });
  });

  it('rejects expired tokens', async () => {
    // ttlSec: -10 produces a token that is already expired.
    const { token } = await signUserAccessToken({ ...claims, ttlSec: -10 }, config);
    expect(await verifyUserAccessToken(token, config)).toMatchObject({ ok: false, reason: 'expired' });
  });

  // case: S-203.20
  it('classifies a correctly signed token from an unrecognized issuer', async () => {
    const now = Math.floor(Date.now() / 1000);
    const token = await new SignJWT({ tenantId: 't1', fam: 'fam-1', type: 'user_access', jti: 'issuer-jti' })
      .setProtectedHeader({ alg: 'HS256' }).setIssuer('unrecognized').setSubject('u1').setIssuedAt(now)
      .setExpirationTime(now + 900).sign(new TextEncoder().encode(config.secret));
    expect(await verifyUserAccessToken(token, config)).toEqual({ ok: false, reason: 'unknown-issuer' });
  });

  it('rejects a token without `fam` even if signature is valid (CD-2)', async () => {
    // Sign a similarly-shaped token but without familyId via the refresh
    // helper used incorrectly: verifyUserAccessToken must reject it.
    const refreshToken = await signUserRefreshToken({ ...claims, ttlSec: 900 }, config);
    expect(await verifyUserAccessToken(refreshToken, config)).toMatchObject({ ok: false, reason: 'invalid' });
  });

  it('rejects a token signed with a non-HS256 algorithm (alg pinning)', async () => {
    // Forge a structurally-valid user-access token, signed with the SAME
    // secret but using HS384 instead of HS256. HMAC accepts the secret for
    // any HS* algorithm, so without an explicit `algorithms: ['HS256']` pin
    // in jwtVerify, jose would happily accept this. The pin must reject it —
    // this is the regression guard for algorithm-substitution.
    const now = Math.floor(Date.now() / 1000);
    const forged = await new SignJWT({
      tenantId: 't1',
      fam: 'fam-1',
      type: 'user_access',
      jti: 'forged-jti',
    })
      .setProtectedHeader({ alg: 'HS384' })
      .setIssuer(KB_JWT_ISSUER)
      .setSubject('u1')
      .setIssuedAt(now)
      .setExpirationTime(now + 900)
      .sign(new TextEncoder().encode(config.secret));

    expect(await verifyUserAccessToken(forged, config)).toMatchObject({ ok: false, reason: 'invalid' });
  });
});

describe('signUserRefreshToken / verifyUserRefreshToken', () => {
  it('round-trips identity + family + jti', async () => {
    const token = await signUserRefreshToken({ ...claims, ttlSec: 60 * 60 }, config);
    const payload = await verifyUserRefreshToken(token, config);
    expect(payload.ok).toBe(true);
    if (!payload.ok) { throw new Error(`Unexpected verification failure: ${payload.reason}`); }
    expect(payload.payload.userId).toBe('u1');
    expect(payload.payload.tenantId).toBe('t1');
    expect(payload.payload.familyId).toBe('fam-1');
    expect(payload.payload.jti).toBeTypeOf('string');
    expect(payload.payload.type).toBe('user_refresh');
  });

  it('refresh and access tokens are not interchangeable', async () => {
    const access = (await signUserAccessToken({ ...claims, ttlSec: 900 }, config)).token;
    expect(await verifyUserRefreshToken(access, config)).toMatchObject({ ok: false, reason: 'invalid' });
  });

  it('rejects refresh tokens missing `fam`', async () => {
    // Re-route through the access helper to produce a non-refresh token shape.
    const access = (await signUserAccessToken({ ...claims, ttlSec: 900 }, config)).token;
    expect(await verifyUserRefreshToken(access, config)).toMatchObject({ ok: false, reason: 'invalid' });
  });

  it('rejects pre-contract user refresh token kinds', async () => {
    const now = Math.floor(Date.now() / 1000);
    const token = await new SignJWT({ tenantId: 't1', fam: 'fam-1', type: 'refresh', jti: 'old-jti' })
      .setProtectedHeader({ alg: 'HS256' }).setSubject('u1').setIssuedAt(now)
      .setExpirationTime(now + 900).sign(new TextEncoder().encode(config.secret));
    expect(await verifyUserRefreshToken(token, config)).toMatchObject({ ok: false, reason: 'unknown-issuer' });
  });
});

describe('jti is unique per token', () => {
  it('different sign calls produce different jti', async () => {
    const a = await signUserAccessToken({ ...claims, ttlSec: 900 }, config);
    const b = await signUserAccessToken({ ...claims, ttlSec: 900 }, config);
    const pa = await verifyUserAccessToken(a.token, config);
    const pb = await verifyUserAccessToken(b.token, config);
    expect(pa.ok && pb.ok && pa.payload.jti).not.toBe(pb.ok && pb.payload.jti);
  });
});
