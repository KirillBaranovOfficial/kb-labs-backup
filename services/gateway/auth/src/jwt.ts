/**
 * JWT sign / verify / refresh logic.
 * Pure functions — no side effects, no ICache dependency.
 */

import { SignJWT, jwtVerify, errors, type JWTPayload } from 'jose';
import { randomUUID } from 'node:crypto';
import { JwtPayloadSchema, type JwtPayload } from '@kb-labs/gateway-contracts';

const ACCESS_TOKEN_TTL = 15 * 60;       // 15 minutes in seconds
const REFRESH_TOKEN_TTL = 30 * 24 * 3600; // 30 days in seconds
export const KB_JWT_ISSUER = 'kb-labs';

// Refresh token schemas stay strict, while `type` selects the verifier.
const MACHINE_REFRESH_CLAIMS = ['iss', 'sub', 'iat', 'exp', 'type', 'jti'] as const;
const USER_REFRESH_CLAIMS = ['iss', 'sub', 'iat', 'exp', 'tenantId', 'fam', 'type', 'jti'] as const;

export type RefreshTokenKind =
  | { kind: 'machine'; hostId: string }
  | { kind: 'user'; userId: string; tenantId: string; familyId: string; jti: string; iat: number; exp: number };

export interface JwtConfig {
  /** Raw secret string — converted to TextEncoder internally */
  secret: string;
}

function getSecretKey(secret: string): Uint8Array {
  return new TextEncoder().encode(secret);
}

function verificationFailure(error: unknown): TokenVerification<never> {
  if (error instanceof errors.JWTExpired) { return { ok: false, reason: 'expired' }; }
  if (error instanceof errors.JWTClaimValidationFailed && error.claim === 'iss') {
    return { ok: false, reason: 'unknown-issuer' };
  }
  return { ok: false, reason: 'invalid' };
}

export interface SignAccessTokenOptions {
  hostId: string;
  namespaceId: string;
  tier: 'free' | 'pro' | 'enterprise';
  type: 'machine_access';
  /** Permissions to embed in the JWT payload. Defaults to [] when absent. */
  permissions?: string[];
}

export interface MachineAccessPayload extends Omit<JwtPayload, 'type'> {
  type: 'machine_access';
}

export async function signAccessToken(
  opts: SignAccessTokenOptions,
  config: JwtConfig,
): Promise<{ token: string; expiresIn: number }> {
  const key = getSecretKey(config.secret);
  const now = Math.floor(Date.now() / 1000);

  const token = await new SignJWT({
    namespaceId: opts.namespaceId,
    tier: opts.tier,
    type: opts.type,
    permissions: opts.permissions ?? [],
  })
    .setProtectedHeader({ alg: 'HS256' })
    .setIssuer(KB_JWT_ISSUER)
    .setSubject(opts.hostId)
    .setIssuedAt(now)
    .setExpirationTime(now + ACCESS_TOKEN_TTL)
    .sign(key);

  return { token, expiresIn: ACCESS_TOKEN_TTL };
}

export async function signRefreshToken(
  hostId: string,
  config: JwtConfig,
): Promise<string> {
  const key = getSecretKey(config.secret);
  const now = Math.floor(Date.now() / 1000);
  const jti = randomUUID();

  return new SignJWT({ type: 'machine_refresh', jti })
    .setProtectedHeader({ alg: 'HS256' })
    .setIssuer(KB_JWT_ISSUER)
    .setSubject(hostId)
    .setIssuedAt(now)
    .setExpirationTime(now + REFRESH_TOKEN_TTL)
    .sign(key);
}

export async function verifyAccessToken(
  token: string,
  config: JwtConfig,
): Promise<TokenVerification<MachineAccessPayload>> {
  try {
    const key = getSecretKey(config.secret);
    const { payload } = await jwtVerify(token, key, { algorithms: ['HS256'], issuer: KB_JWT_ISSUER });
    const parsed = JwtPayloadSchema.safeParse(payload);
    if (!parsed.success || parsed.data.type !== 'machine_access') {return { ok: false, reason: 'invalid' };}
    return { ok: true, payload: { ...parsed.data, type: 'machine_access' } };
  } catch (error) {
    return verificationFailure(error);
  }
}

export async function verifyRefreshToken(
  token: string,
  config: JwtConfig,
): Promise<TokenVerification<{ hostId: string }>> {
  const verified = await verifyRefreshTokenKind(token, config);
  if (!verified.ok) { return verified; }
  return verified.payload.kind === 'machine'
    ? { ok: true, payload: { hostId: verified.payload.hostId } }
    : { ok: false, reason: 'invalid' };
}

// ─── User-flavoured tokens (ADR-0020, Phase 1.13) ────────────────────────
//
// These live alongside the machine-token helpers above. Each token uses its
// own explicit kind. `fam` (familyId) is **mandatory**
// in BOTH access and refresh — verifyUserAccessToken rejects tokens
// without it even on otherwise valid signatures. Without `fam` in the
// access cookie, /auth/password/change cannot identify the current
// session family to spare on revoke-all-others (CD-2).
//
// Payload never contains role / scopes / permissions (principle #3).
// Authorization is resolved per-request by the PDP.

export interface SignUserTokenOptions {
  userId: string;
  tenantId: string;
  familyId: string;
  /** TTL in seconds. */
  ttlSec: number;
}

export interface UserAccessPayload {
  userId: string;
  tenantId: string;
  familyId: string;
  jti: string;
  /** Explicit token kind discriminator. */
  type: 'user_access';
  iat: number;
  exp: number;
}

export type TokenVerification<T> =
  | { ok: true; payload: T }
  | { ok: false; reason: 'expired' | 'invalid' | 'unknown-issuer' | 'inactive' | 'revoked' };

export interface UserRefreshPayload {
  userId: string;
  tenantId: string;
  familyId: string;
  jti: string;
  type: 'user_refresh';
  iat: number;
  exp: number;
}

export interface UserAccessSignResult {
  token: string;
  expiresInSec: number;
  jti: string;
}

export async function signUserAccessToken(
  opts: SignUserTokenOptions,
  config: JwtConfig,
): Promise<UserAccessSignResult> {
  const key = getSecretKey(config.secret);
  const now = Math.floor(Date.now() / 1000);
  const jti = randomUUID();
  const token = await new SignJWT({
    tenantId: opts.tenantId,
    fam: opts.familyId,
    type: 'user_access',
    jti,
  })
    .setProtectedHeader({ alg: 'HS256' })
    .setIssuer(KB_JWT_ISSUER)
    .setSubject(opts.userId)
    .setIssuedAt(now)
    .setExpirationTime(now + opts.ttlSec)
    .sign(key);
  return { token, expiresInSec: opts.ttlSec, jti };
}

/**
 * Sign a user refresh token.
 *
 * When the caller already owns the `jti` (typical: the sessions-store
 * minted it inside `createSession`/`rotateRefresh`), pass it via the
 * second argument so the JWT and the store agree on the identifier.
 * Otherwise the helper generates one — used by tests that don't go
 * through sessions-store.
 */
export async function signUserRefreshToken(
  opts: SignUserTokenOptions & { jti?: string },
  config: JwtConfig,
): Promise<string> {
  const key = getSecretKey(config.secret);
  const now = Math.floor(Date.now() / 1000);
  const jti = opts.jti ?? randomUUID();
  return new SignJWT({
    tenantId: opts.tenantId,
    fam: opts.familyId,
    type: 'user_refresh',
    jti,
  })
    .setProtectedHeader({ alg: 'HS256' })
    .setIssuer(KB_JWT_ISSUER)
    .setSubject(opts.userId)
    .setIssuedAt(now)
    .setExpirationTime(now + opts.ttlSec)
    .sign(key);
}

interface RawUserClaims extends JWTPayload {
  tenantId?: string;
  fam?: string;
  type?: string;
  jti?: string;
}

const hasExactClaims = (payload: JWTPayload, claimNames: readonly string[]): boolean => {
  const actual = Object.keys(payload);
  return actual.length === claimNames.length && claimNames.every((claim) => actual.includes(claim));
};

const normaliseAccess = (raw: RawUserClaims): UserAccessPayload | null => {
  if (raw.type !== 'user_access') {return null;}
  if (!raw.sub || !raw.tenantId || !raw.fam || !raw.jti) {return null;}
  if (typeof raw.iat !== 'number' || typeof raw.exp !== 'number') {return null;}
  return {
    userId: raw.sub,
    tenantId: raw.tenantId,
    familyId: raw.fam,
    jti: raw.jti,
    type: 'user_access',
    iat: raw.iat,
    exp: raw.exp,
  };
};

const normaliseRefresh = (raw: RawUserClaims): UserRefreshPayload | null => {
  if (!hasExactClaims(raw, USER_REFRESH_CLAIMS)) {return null;}
  if (raw.type !== 'user_refresh') {return null;}
  if (!raw.sub || !raw.tenantId || !raw.fam || !raw.jti) {return null;}
  if (typeof raw.iat !== 'number' || typeof raw.exp !== 'number' || !Number.isInteger(raw.iat)
    || !Number.isInteger(raw.exp) || raw.exp <= raw.iat) {return null;}
  return {
    userId: raw.sub,
    tenantId: raw.tenantId,
    familyId: raw.fam,
    jti: raw.jti,
    type: 'user_refresh',
    iat: raw.iat,
    exp: raw.exp,
  };
};

const normaliseMachineRefresh = (raw: JWTPayload): RefreshTokenKind | null => {
  if (!hasExactClaims(raw, MACHINE_REFRESH_CLAIMS)) {return null;}
  if (raw.type !== 'machine_refresh' || typeof raw.sub !== 'string' || !raw.sub || typeof raw.jti !== 'string' || !raw.jti) {return null;}
  if (typeof raw.iat !== 'number' || typeof raw.exp !== 'number' || !Number.isInteger(raw.iat)
    || !Number.isInteger(raw.exp) || raw.exp <= raw.iat) {return null;}
  return { kind: 'machine', hostId: raw.sub };
};

export async function verifyRefreshTokenKind(
  token: string,
  config: JwtConfig,
): Promise<TokenVerification<RefreshTokenKind>> {
  try {
    const key = getSecretKey(config.secret);
    const { payload } = await jwtVerify(token, key, { algorithms: ['HS256'], issuer: KB_JWT_ISSUER });
    if (payload.type === 'machine_refresh') {
      const machine = normaliseMachineRefresh(payload);
      return machine ? { ok: true, payload: machine } : { ok: false, reason: 'invalid' };
    }
    if (payload.type === 'user_refresh') {
      const user = normaliseRefresh(payload as RawUserClaims);
      if (!user) {return { ok: false, reason: 'invalid' };}
      return { ok: true, payload: {
        kind: 'user',
        userId: user.userId,
        tenantId: user.tenantId,
        familyId: user.familyId,
        jti: user.jti,
        iat: user.iat,
        exp: user.exp,
      } };
    }
    return { ok: false, reason: 'invalid' };
  } catch (error) {
    return verificationFailure(error);
  }
}

export async function verifyUserAccessToken(
  token: string,
  config: JwtConfig,
): Promise<TokenVerification<UserAccessPayload>> {
  try {
    const key = getSecretKey(config.secret);
    const { payload } = await jwtVerify(token, key, { algorithms: ['HS256'], issuer: KB_JWT_ISSUER });
    const normalised = normaliseAccess(payload as RawUserClaims);
    return normalised ? { ok: true, payload: normalised } : { ok: false, reason: 'invalid' };
  } catch (error) {
    return verificationFailure(error);
  }
}

export async function verifyUserRefreshToken(
  token: string,
  config: JwtConfig,
): Promise<TokenVerification<UserRefreshPayload>> {
  const verified = await verifyRefreshTokenKind(token, config);
  if (!verified.ok || verified.payload.kind !== 'user') {
    return !verified.ok ? verified : { ok: false, reason: 'invalid' };
  }
  return { ok: true, payload: {
    userId: verified.payload.userId,
    tenantId: verified.payload.tenantId,
    familyId: verified.payload.familyId,
    jti: verified.payload.jti,
    type: 'user_refresh',
    iat: verified.payload.iat,
    exp: verified.payload.exp,
  } };
}
