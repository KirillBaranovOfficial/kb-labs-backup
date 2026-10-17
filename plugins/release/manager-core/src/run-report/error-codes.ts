import {
  createValidatedErrorEnvelope,
  getCatalogEntry,
  type DetailsOptionFor,
  type ErrorAction,
  type ErrorCatalogEntry,
  type ErrorEnvelope,
  type PlatformErrorCode,
} from '@kb-labs/core-platform/error-envelope';

export type ReleaseErrorCode = Extract<PlatformErrorCode, `KB_RELEASE_${string}`>;

export function describeReleaseError(code: ReleaseErrorCode): ErrorCatalogEntry {
  const entry = getCatalogEntry(code);
  if (!entry) {
    throw new Error(`Release error code ${code} is missing from the platform error catalog.`);
  }
  return entry;
}

export type CreateReleaseErrorInput<C extends ReleaseErrorCode = ReleaseErrorCode> = {
  code: C;
  message?: string;
  cause?: string;
  hint?: string;
  actions?: ErrorAction[];
  severity?: 'error' | 'warning';
  correlationId?: string;
} & DetailsOptionFor<C>;

/** Build a release failure from the shared platform catalog. */
export function createReleaseError<C extends ReleaseErrorCode>(input: CreateReleaseErrorInput<C>): ErrorEnvelope {
  describeReleaseError(input.code);
  return createValidatedErrorEnvelope(input.code, {
    ...(input.message !== undefined ? { message: input.message } : {}),
    ...(input.cause !== undefined ? { cause: input.cause } : {}),
    ...(input.hint !== undefined ? { hint: input.hint } : {}),
    ...(input.actions !== undefined ? { actions: input.actions } : {}),
    ...(input.correlationId !== undefined ? { correlationId: input.correlationId } : {}),
    ...(input.details !== undefined ? { details: input.details } : {}),
    ...(input.severity !== undefined ? { severity: input.severity } : {}),
  });
}
