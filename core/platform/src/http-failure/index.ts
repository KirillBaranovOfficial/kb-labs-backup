import { createErrorEnvelope, getCatalogEntry, isErrorCode, toErrorEnvelope, type ErrorCode, type ErrorEnvelope } from '../error-envelope/index.js';
import { createUxFailureResult, type UxFailureResult } from '../ux-result/index.js';

export interface HttpFailureOptions {
  correlationId: string;
  statusCode: number;
}

export function statusForHttpFailure(payload: unknown, transportStatusCode: number): number {
  const parts = failureParts(payload, transportStatusCode);
  if (!parts.cataloguedPayload || !isErrorCode(parts.code)) return transportStatusCode;

  const entry = getCatalogEntry(parts.code);
  if (!entry) {
    throw new Error(`HTTP failure code "${parts.code}" is not in errors.catalog.json`);
  }
  if (entry.httpStatus === undefined) {
    throw new Error(`Catalogued error "${parts.code}" reached an HTTP boundary without httpStatus; add httpStatus to errors.catalog.json`);
  }
  return entry.httpStatus;
}

interface HttpFailureParts {
  code: ErrorCode;
  cataloguedPayload: boolean;
  error: ErrorEnvelope;
}

function failureParts(payload: unknown, statusCode: number): HttpFailureParts {
  const nested = payload && typeof payload === 'object' && 'error' in payload ? payload.error : undefined;
  const candidate = hasCode(nested) ? nested : hasCode(payload) ? payload : undefined;
  if (candidate !== undefined) {
    const error = toErrorEnvelope(candidate);
    return { code: error.code, cataloguedPayload: isErrorCode(candidate.code), error };
  }
  const code = statusCode === 401 ? 'KB_AUTH_REQUIRED'
    : statusCode === 403 ? 'KB_AUTH_FORBIDDEN'
      : statusCode === 400 ? 'KB_RUNTIME_INPUT_INVALID'
        : 'KB_RUNTIME_UNEXPECTED';
  return { code, cataloguedPayload: false, error: createErrorEnvelope(code) };
}

function hasCode(value: unknown): value is Record<string, unknown> & { code: string } {
  return typeof value === 'object' && value !== null && 'code' in value && typeof value.code === 'string';
}

/** Select the catalog code for an HTTP failure without constructing its result envelope. */
export function codeForHttpFailure(payload: unknown, statusCode: number): ErrorCode {
  return failureParts(payload, statusCode).code;
}

/** Convert every HTTP failure shape to the ADR-0049 error result once. */
export function normalizeHttpFailure(payload: unknown, options: HttpFailureOptions): UxFailureResult {
  const parts = failureParts(payload, options.statusCode);
  const error = { ...parts.error, correlationId: parts.error.correlationId || options.correlationId };
  return createUxFailureResult(error);
}
