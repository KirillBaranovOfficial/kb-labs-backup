import { ErrorEnvelopeJsonSchema } from '@kb-labs/core-platform/error-envelope';

const uxErrorStatuses = [400, 401, 403, 404, 409, 422, 429, 500, 501, 502, 503] as const;
type UxErrorStatus = (typeof uxErrorStatuses)[number];

/** Route handlers send this error body before the gateway boundary wraps it. */
const gatewayHandlerErrorResponse = {
  type: 'object',
  properties: {
    error: { anyOf: [{ type: 'string' }, ErrorEnvelopeJsonSchema] },
    message: { type: 'string' },
    hint: { type: 'string' },
    permission: { type: 'string' },
    code: { type: 'string' },
    details: { type: 'object', additionalProperties: true },
    retryAfter: { type: 'integer' },
    limit: { type: 'integer' },
    resetAt: { type: 'string' },
  },
  required: ['error'],
  additionalProperties: false,
} as const;

/** Add the handler error body expected before the UX boundary to each error status. */
export function withUxErrorResponses<const Success extends Record<number, unknown>>(
  successResponses: Success,
): Success & Record<UxErrorStatus, typeof gatewayHandlerErrorResponse> {
  return Object.assign(
    {},
    Object.fromEntries(uxErrorStatuses.map((status) => [status, gatewayHandlerErrorResponse])),
    successResponses,
  ) as Success & Record<UxErrorStatus, typeof gatewayHandlerErrorResponse>;
}

const noBodySchema = {
  anyOf: [
    { type: 'object', maxProperties: 0, additionalProperties: false },
    { type: 'null' },
  ],
} as const;

/** A route body that may be omitted on requests handled through a cookie. */
export function optionalRequestBodySchema<const Body extends Record<string, unknown>>(body: Body) {
  return { anyOf: [body, noBodySchema] } as const;
}
