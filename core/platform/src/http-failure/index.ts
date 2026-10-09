import { createErrorEnvelope, getCatalogEntry, isErrorEnvelope, type ErrorEnvelope } from '../error-envelope/index.js';
import { createUxFailureResult, isUxResult, type UxFailureResult } from '../ux-result/index.js';

export interface HttpFailureOptions {
  correlationId: string;
  statusCode: number;
}

export function statusForHttpFailure(payload: unknown, transportStatusCode: number): number {
  const parts = failureParts(payload, transportStatusCode);
  if (!parts.cataloguedPayload) return transportStatusCode;

  const entry = getCatalogEntry(parts.code);
  if (!entry) {
    throw new Error(`HTTP failure code "${parts.code}" is not in errors.catalog.json`);
  }
  if (entry.httpStatus === undefined) {
    throw new Error(`Catalogued error "${parts.code}" reached an HTTP boundary without httpStatus; add httpStatus to errors.catalog.json`);
  }
  return entry.httpStatus;
}

function rawCode(value: unknown): string | undefined {
  if (!value || typeof value !== 'object' || !('code' in value)) return undefined;
  return typeof value.code === 'string' ? value.code : undefined;
}

interface HttpFailureParts {
  code: string;
  cataloguedPayload: boolean;
  error?: ErrorEnvelope;
}

function failureParts(payload: unknown, statusCode: number): HttpFailureParts {
  if (isUxResult(payload) && !payload.ok && getCatalogEntry(payload.error.code)) {
    return { code: payload.error.code, cataloguedPayload: true, error: payload.error };
  }

  const nestedError = payload && typeof payload === 'object' && 'error' in payload ? payload.error : undefined;
  const typedError = isErrorEnvelope(nestedError) && getCatalogEntry(nestedError.code)
    ? nestedError
    : isErrorEnvelope(payload) && getCatalogEntry(payload.code) ? payload : undefined;
  const nestedCode = rawCode(nestedError);
  const directCode = rawCode(payload);
  const code = typedError?.code
    ?? (nestedCode && getCatalogEntry(nestedCode) ? nestedCode : undefined)
    ?? (directCode && getCatalogEntry(directCode) ? directCode : undefined)
    ?? (statusCode === 401 ? 'KB_AUTH_REQUIRED'
      : statusCode === 403 ? 'KB_AUTH_FORBIDDEN'
        : statusCode === 400 ? 'KB_RUNTIME_INPUT_INVALID' : 'KB_RUNTIME_UNEXPECTED');
  const cataloguedPayload = Boolean(
    typedError
    || (nestedCode && getCatalogEntry(nestedCode))
    || (directCode && getCatalogEntry(directCode)),
  );
  return { code, cataloguedPayload, ...(typedError ? { error: typedError } : {}) };
}

/** Select the catalog code for an HTTP failure without constructing its result envelope. */
export function codeForHttpFailure(payload: unknown, statusCode: number): string {
  return failureParts(payload, statusCode).code;
}

/** Convert every HTTP failure shape to the ADR-0049 error result once. */
export function normalizeHttpFailure(payload: unknown, options: HttpFailureOptions): UxFailureResult {
  const parts = failureParts(payload, options.statusCode);
  const error = parts.error
    ? { ...parts.error, correlationId: parts.error.correlationId || options.correlationId }
    : createErrorEnvelope(parts.code, { correlationId: options.correlationId });
  return createUxFailureResult(error);
}
