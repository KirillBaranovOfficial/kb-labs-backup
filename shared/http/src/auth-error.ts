import type { FastifyReply } from 'fastify';
import { createErrorEnvelope } from '@kb-labs/core-platform';

/** Send one catalog-backed authentication or authorization failure response. */
export function sendAuthError(
  reply: FastifyReply,
  status: 400 | 401 | 403 | 409 | 422,
  permission?: string,
  cause?: string,
  code?: 'KB_AUTH_REQUIRED' | 'KB_AUTH_FORBIDDEN' | 'KB_AUTH_INVALID_CREDENTIALS'
    | 'KB_AUTH_INVALID_INVITE' | 'KB_AUTH_INVALID_PASSWORD_RESET' | 'KB_AUTH_INVALID_CURRENT_PASSWORD'
    | 'KB_AUTH_LAST_ADMIN',
  responseFields: Record<string, unknown> = {},
) {
  const errorCode = code ?? (status === 403 ? 'KB_AUTH_FORBIDDEN' : 'KB_AUTH_REQUIRED');
  const error = errorCode === 'KB_AUTH_FORBIDDEN'
    ? createErrorEnvelope('KB_AUTH_FORBIDDEN', {
      details: { permission: permission ?? 'the requested operation' },
      ...(cause ? { cause } : {}),
    })
    : errorCode === 'KB_AUTH_REQUIRED'
      ? createErrorEnvelope('KB_AUTH_REQUIRED', {
        details: { gatewayUrl: '<url>' },
        ...(cause ? { cause } : {}),
      })
      : createErrorEnvelope(errorCode, { ...(cause ? { cause } : {}) });
  return reply.code(status).send({ ok: false, error, ...responseFields });
}
