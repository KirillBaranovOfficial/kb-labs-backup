import {
  createErrorEnvelope,
  getCatalogEntry,
  type ErrorAction,
  type ErrorCatalogEntry,
  type ErrorCode,
  type ErrorEnvelope,
} from '@kb-labs/core-platform/error-envelope';

export type ReleaseErrorCode = Extract<ErrorCode, `KB_RELEASE_${string}`>;

export function describeReleaseError(code: ReleaseErrorCode): ErrorCatalogEntry {
  const entry = getCatalogEntry(code);
  if (!entry) {
    throw new Error(`Release error code ${code} is missing from the platform error catalog.`);
  }
  return entry;
}

export interface CreateReleaseErrorInput {
  code: ReleaseErrorCode;
  message?: string;
  cause?: string;
  hint?: string;
  actions?: ErrorAction[];
  severity?: 'error' | 'warning';
  correlationId?: string;
  details?: Record<string, string>;
}

/** Build a release failure from the shared platform catalog. */
export function createReleaseError(input: CreateReleaseErrorInput): ErrorEnvelope {
  const entry = describeReleaseError(input.code);
  return createErrorEnvelope(input.code, {
    ...(input.message !== undefined ? { message: input.message } : {}),
    ...(input.cause !== undefined ? { cause: input.cause } : {}),
    ...(input.hint !== undefined ? { hint: input.hint } : {}),
    ...(input.actions !== undefined ? { actions: input.actions } : {}),
    ...(input.correlationId !== undefined ? { correlationId: input.correlationId } : {}),
    ...(input.details !== undefined ? { details: input.details } : {}),
    ...(input.severity !== undefined ? { severity: input.severity } : {}),
  });
}
