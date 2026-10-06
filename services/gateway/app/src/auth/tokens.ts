import { createErrorEnvelope, type ErrorEnvelope, type ICache } from '@kb-labs/core-platform';
import type { AuthContext } from '@kb-labs/gateway-contracts';
import { AuthService, type JwtConfig } from '@kb-labs/gateway-auth';
import type { TokenVerification } from '@kb-labs/gateway-auth';
import type { FastifyRequest } from 'fastify';

export type AuthRejectionReason = 'expired' | 'invalid' | 'unknown-issuer' | 'inactive' | 'revoked';

export function gatewayRequestUrl(request: FastifyRequest): string {
  return `${request.protocol}://${request.headers.host ?? request.hostname}`;
}

export function authRejectionEnvelope(
  reason: AuthRejectionReason | undefined,
  gatewayUrl: string,
  correlationId: string,
): ErrorEnvelope {
  const code = reason === 'expired'
    ? 'KB_AUTH_TOKEN_EXPIRED'
    : reason === 'revoked'
      ? 'KB_AUTH_SESSION_REVOKED'
      : reason === 'inactive'
        ? 'KB_AUTH_USER_INACTIVE'
        : 'KB_AUTH_REQUIRED';
  return createErrorEnvelope(code, {
    details: { gatewayUrl, ...(reason ? { reason } : {}) },
    correlationId,
  });
}

/**
 * Resolve a Bearer token to an AuthContext.
 *
 * Priority:
 *   1. JWT verification (machine tokens issued via /auth/token).
 *   2. Host-registry opaque token (issued by /hosts/register, stored in cache
 *      as host:token:{uuid}). These are runtime-issued tokens for WebSocket agents,
 *      distinct from the removed static pre-seeded config tokens.
 */
export async function resolveToken(
  token: string,
  cache: ICache,
  jwtConfig: JwtConfig,
): Promise<TokenVerification<AuthContext>> {
  const authService = new AuthService(cache, jwtConfig);

  // Try JWT first (tokens issued via /auth/token).
  const jwtContext = await authService.verify(token);
  if (jwtContext.ok) {return jwtContext;}

  // Fallback: host-registry opaque token (issued by /hosts/register).
  // Permissions default to ['host:connect'] for backward compatibility.
  const hostEntry = await cache.get<{ hostId: string; namespaceId: string; permissions?: string[] }>(
    `host:token:${token}`,
  );
  if (hostEntry) {
    return { ok: true, payload: {
      type: 'machine',
      userId: hostEntry.hostId,
      namespaceId: hostEntry.namespaceId,
      tier: 'free',
      permissions: hostEntry.permissions ?? ['host:connect'],
    } };
  }

  return jwtContext;
}

export function extractBearerToken(authHeader: string | undefined): string | null {
  if (!authHeader) {return null;}
  const match = authHeader.match(/^Bearer\s+(.+)$/i);
  return match ? (match[1] ?? null) : null;
}
