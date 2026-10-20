import { createHash, timingSafeEqual } from 'node:crypto';
import { AuthContextSchema, type AuthContext } from './auth.js';

export const INTERNAL_SECRET_HEADER = 'x-internal-secret';

export const FORWARDED_IDENTITY_HEADER_NAMES = [
  'x-kb-auth-type',
  'x-kb-auth-user-id',
  'x-kb-auth-token-id',
  'x-kb-auth-namespace-id',
  'x-kb-auth-tier',
  'x-kb-auth-permissions',
] as const;

const [TYPE_HEADER, USER_ID_HEADER, TOKEN_ID_HEADER, NAMESPACE_ID_HEADER, TIER_HEADER, PERMISSIONS_HEADER] =
  FORWARDED_IDENTITY_HEADER_NAMES;

export type ForwardedIdentityHeaders = Readonly<
  Record<string, string | string[] | undefined>
>;

export type ForwardedIdentityResult =
  | { kind: 'identity'; auth: AuthContext }
  | { kind: 'service' }
  | { kind: 'rejected' };

function header(headers: ForwardedIdentityHeaders, name: string): string | undefined {
  const value = headers[name];
  return typeof value === 'string' ? value : undefined;
}

function secretMatches(expected: string, received: string | undefined): boolean {
  if (!received) { return false; }
  const expectedDigest = createHash('sha256').update(expected).digest();
  const receivedDigest = createHash('sha256').update(received).digest();
  return timingSafeEqual(expectedDigest, receivedDigest);
}

/** Encode the gateway-resolved identity for a trusted internal service request. */
export function forwardedIdentityHeaders(
  auth: AuthContext | undefined,
  secret: string | undefined,
): Record<string, string> {
  if (!secret) { return {}; }
  const headers: Record<string, string> = { [INTERNAL_SECRET_HEADER]: secret };
  if (!auth) { return headers; }
  headers[TYPE_HEADER] = auth.type;
  headers[USER_ID_HEADER] = auth.userId;
  if (auth.tokenId) { headers[TOKEN_ID_HEADER] = auth.tokenId; }
  headers[NAMESPACE_ID_HEADER] = auth.namespaceId;
  headers[TIER_HEADER] = auth.tier;
  headers[PERMISSIONS_HEADER] = JSON.stringify(auth.permissions);
  return headers;
}

/** Remove all client-supplied internal identity material before gateway auth. */
export function stripForwardedIdentityHeaders(
  headers: Record<string, unknown>,
): void {
  delete headers[INTERNAL_SECRET_HEADER];
  for (const name of FORWARDED_IDENTITY_HEADER_NAMES) { delete headers[name]; }
}

/** Decode a trusted gateway identity, or a service-only internal request. */
export function readForwardedIdentity(
  headers: ForwardedIdentityHeaders,
  secret: string | undefined,
): ForwardedIdentityResult {
  if (!secret || !secretMatches(secret, header(headers, INTERNAL_SECRET_HEADER))) {
    return { kind: 'rejected' };
  }

  const hasIdentity = FORWARDED_IDENTITY_HEADER_NAMES.some((name) => headers[name] !== undefined);
  if (!hasIdentity) { return { kind: 'service' }; }

  const permissionsJson = header(headers, PERMISSIONS_HEADER);
  let permissions: unknown;
  try {
    permissions = permissionsJson === undefined ? undefined : JSON.parse(permissionsJson);
  } catch {
    return { kind: 'rejected' };
  }

  const parsed = AuthContextSchema.safeParse({
    type: header(headers, TYPE_HEADER),
    userId: header(headers, USER_ID_HEADER),
    tokenId: header(headers, TOKEN_ID_HEADER),
    namespaceId: header(headers, NAMESPACE_ID_HEADER),
    tier: header(headers, TIER_HEADER),
    permissions,
  });
  return parsed.success ? { kind: 'identity', auth: parsed.data } : { kind: 'rejected' };
}
