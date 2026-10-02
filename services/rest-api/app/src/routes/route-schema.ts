import { UxFailureResultJsonSchema } from '@kb-labs/core-platform/error-envelope';

const uxErrorStatuses = [400, 401, 403, 404, 409, 422, 429, 500, 501, 503] as const;
type UxErrorStatus = (typeof uxErrorStatuses)[number];

/** JSON representation of requests whose handlers consume no payload. */
export const noBodySchema = {
  anyOf: [
    { type: 'object', maxProperties: 0, additionalProperties: false },
    { type: 'null' },
  ],
} as const;

/** Add the shared failure result to the statuses used by route handlers. */
export function withUxErrorResponses<const Success extends Record<number, unknown>>(
  successResponses: Success,
): Success & Record<UxErrorStatus, typeof UxFailureResultJsonSchema> {
  return Object.assign(
    {},
    Object.fromEntries(uxErrorStatuses.map((status) => [status, UxFailureResultJsonSchema])),
    successResponses,
  ) as Success & Record<UxErrorStatus, typeof UxFailureResultJsonSchema>;
}
