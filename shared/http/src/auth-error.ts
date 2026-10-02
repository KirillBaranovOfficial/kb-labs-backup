import type { FastifyReply } from 'fastify';
import { createErrorEnvelope } from '@kb-labs/core-platform';

/** Send one catalog-backed authentication or authorization failure response. */
export function sendAuthError(
  reply: FastifyReply,
  status: 401 | 403,
  permission?: string,
  cause?: string,
  code?: 'KB_AUTH_REQUIRED' | 'KB_AUTH_FORBIDDEN' | 'KB_AUTH_INVALID_CREDENTIALS',
  responseFields: Record<string, unknown> = {},
) {
  const errorCode = code ?? (status === 403 ? 'KB_AUTH_FORBIDDEN' : 'KB_AUTH_REQUIRED');
  const error = errorCode === 'KB_AUTH_FORBIDDEN'
    ? createErrorEnvelope('KB_AUTH_FORBIDDEN', permission
      ? { details: { permission }, ...(cause ? { cause } : {}) }
      : { message: 'You do not have permission to perform this operation.', ...(cause ? { cause } : {}) })
    : createErrorEnvelope(errorCode, {
      ...(errorCode === 'KB_AUTH_REQUIRED' ? { details: { gatewayUrl: '<url>' } } : {}),
      ...(cause ? { cause } : {}),
    });
  return reply.code(status).send({ ok: false, error, ...responseFields });
}
