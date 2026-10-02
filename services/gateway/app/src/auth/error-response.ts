import type { FastifyReply } from 'fastify';
import { createErrorEnvelope } from '@kb-labs/core-platform';

/** Send a catalog-backed response for a failed authentication or authorization check. */
export function sendAuthError(
  reply: FastifyReply,
  status: 401 | 403,
  permission?: string,
  cause?: string,
  code?: 'KB_AUTH_REQUIRED' | 'KB_AUTH_FORBIDDEN' | 'KB_AUTH_INVALID_CREDENTIALS',
) {
  const errorCode = code ?? (status === 403 ? 'KB_AUTH_FORBIDDEN' : 'KB_AUTH_REQUIRED');
  const error = errorCode === 'KB_AUTH_FORBIDDEN'
    ? createErrorEnvelope('KB_AUTH_FORBIDDEN', permission
      ? { details: { permission } }
      : { message: 'You do not have permission to perform this operation.' })
    : createErrorEnvelope(errorCode, {
      details: { gatewayUrl: '<url>' },
      ...(cause ? { cause } : {}),
    });
  return reply.code(status).send({ ok: false, error });
}
