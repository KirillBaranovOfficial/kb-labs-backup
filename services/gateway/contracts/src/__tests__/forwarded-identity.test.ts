import { describe, expect, it } from 'vitest';
import {
  forwardedIdentityHeaders,
  readForwardedIdentity,
  stripForwardedIdentityHeaders,
} from '../forwarded-identity.js';

const secret = 'test-internal-secret';
const auth = {
  type: 'user' as const,
  userId: 'user-7',
  tokenId: 'token-9',
  namespaceId: 'tenant-4',
  tier: 'pro' as const,
  permissions: ['projects:read', 'users:write'],
};

describe('forwarded gateway identity codec', () => {
  it('round trips gateway-resolved identity and permissions', () => {
    const headers = forwardedIdentityHeaders(auth, secret);
    expect(readForwardedIdentity(headers, secret)).toEqual({ kind: 'identity', auth });
  });

  it('recognizes a valid internal service request without identity', () => {
    expect(readForwardedIdentity(forwardedIdentityHeaders(undefined, secret), secret))
      .toEqual({ kind: 'service' });
  });

  it('rejects a tampered or missing secret', () => {
    const headers = forwardedIdentityHeaders(auth, secret);
    expect(readForwardedIdentity(headers, 'different-secret')).toEqual({ kind: 'rejected' });
    expect(readForwardedIdentity(headers, undefined)).toEqual({ kind: 'rejected' });
    expect(readForwardedIdentity({ ...headers, 'x-internal-secret': 'wrong' }, secret))
      .toEqual({ kind: 'rejected' });
  });

  it('rejects partial identity headers and non-array permissions', () => {
    expect(readForwardedIdentity({
      'x-internal-secret': secret,
      'x-kb-auth-user-id': auth.userId,
    }, secret)).toEqual({ kind: 'rejected' });
    expect(readForwardedIdentity({
      ...forwardedIdentityHeaders(auth, secret),
      'x-kb-auth-permissions': '{"permission":"users:write"}',
    }, secret)).toEqual({ kind: 'rejected' });
  });

  it('round trips the API token identifier as part of the forwarded identity', () => {
    const headers = forwardedIdentityHeaders(auth, secret);
    expect(headers['x-kb-auth-token-id']).toBe('token-9');
    expect(readForwardedIdentity(headers, secret)).toEqual({ kind: 'identity', auth });
  });

  it('strips client-supplied secret and identity headers', () => {
    const headers: Record<string, unknown> = {
      ...forwardedIdentityHeaders(auth, secret),
      authorization: 'Bearer user-token',
    };
    stripForwardedIdentityHeaders(headers);
    expect(headers).toEqual({ authorization: 'Bearer user-token' });
  });
});
