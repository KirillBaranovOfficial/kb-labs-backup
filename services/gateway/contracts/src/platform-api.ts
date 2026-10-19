/**
 * @module @kb-labs/gateway-contracts/platform-api
 * Unified Platform API — single dispatch endpoint for any adapter.
 *
 * Route: POST /platform/v1/{adapter}/{method}
 * Adapter and method come from URL params; args come from the request body.
 */
// ── Request ─────────────────────────────────────────────────────────────────

/** Request schema used by Fastify validation and the hidden route declaration. */
export const PlatformCallRequestJsonSchema = {
  type: 'object',
  properties: {
    /** Arguments to pass to the adapter method. Order matters. */
    args: { type: 'array', items: {} },
  },
  additionalProperties: true,
} as const;

export interface PlatformCallRequest {
  args?: unknown[];
}
