import type { FastifyReply, FastifyRequest } from 'fastify';
import { PERMISSIONS } from '@kb-labs/core-contracts';
import { createErrorEnvelope } from '@kb-labs/core-platform';
import { readForwardedIdentity } from '@kb-labs/gateway-contracts';

/** Require a Gateway-verified tenant administrator for REST operator surfaces. */
export async function requireRestOperator(request: FastifyRequest, reply: FastifyReply): Promise<FastifyReply | void> {
  const identity = readForwardedIdentity(request.headers, process.env.GATEWAY_INTERNAL_SECRET);
  if (identity.kind === 'identity' && identity.auth.permissions.includes(PERMISSIONS.USERS_READ)) {
    return;
  }

  const statusCode = identity.kind === 'rejected' ? 401 : 403;
  const code = identity.kind === 'rejected' ? 'KB_AUTH_REQUIRED' : 'KB_AUTH_FORBIDDEN';
  return reply.code(statusCode).send({
    error: createErrorEnvelope(code, { correlationId: String(request.id) }),
  });
}
