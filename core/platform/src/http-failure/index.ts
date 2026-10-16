import { createErrorEnvelope, getCatalogEntry, isErrorCode, toErrorEnvelope, type ErrorCode, type ErrorEnvelope } from '../error-envelope/index.js';
import type { CatalogErrorCode } from '../error-envelope/error-codes.generated.js';
import { createUxFailureResult, type UxFailureResult } from '../ux-result/index.js';

export interface HttpFailureOptions {
  correlationId: string;
  statusCode: number;
  /** Byte limit enforced by the boundary that produced this HTTP failure. */
  limitBytes?: number;
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

function failureParts(payload: unknown, statusCode: number, limitBytes?: number): HttpFailureParts {
  const nested = payload && typeof payload === 'object' && 'error' in payload ? payload.error : undefined;
  const candidate = hasCode(nested) ? nested : hasCode(payload) ? payload : undefined;
  if (candidate !== undefined) {
    if (statusCode === 413 && isErrorCode(candidate.code)) {
      const code = fallbackCodeForStatus(statusCode);
      return { code, cataloguedPayload: false, error: createFallbackError(code, limitBytes) };
    }
    const error = toErrorEnvelope(candidate);
    if (statusCode !== 413 || error.area === 'product') {
      return { code: error.code, cataloguedPayload: isErrorCode(candidate.code), error };
    }
    const code = fallbackCodeForStatus(statusCode);
    return { code, cataloguedPayload: false, error: createFallbackError(code, limitBytes) };
  }
  const code = fallbackCodeForStatus(statusCode);
  return { code, cataloguedPayload: false, error: createFallbackError(code, limitBytes) };
}

function createFallbackError(code: CatalogErrorCode, limitBytes?: number): ErrorEnvelope {
  if (code === 'KB_AUTH_REQUIRED') {
    return createErrorEnvelope(code, {
      message: 'Authentication is required.',
      hint: 'Run `kb auth login --help` to configure Gateway authentication, then retry.',
    });
  }
  if (code === 'KB_AUTH_FORBIDDEN') {
    return createErrorEnvelope(code, {
      message: 'Permission denied.',
      hint: 'Ask your administrator for permission to perform the requested operation.',
    });
  }
  if (code !== 'KB_RUNTIME_PAYLOAD_TOO_LARGE') return createErrorEnvelope(code);
  if (limitBytes === undefined) {
    return createErrorEnvelope(code, { details: { limit: 'the configured receiving-service limit' } });
  }
  return createErrorEnvelope(code, { details: { limit: formatByteLimit(limitBytes), limitBytes: String(limitBytes) } });
}

function formatByteLimit(limitBytes: number): string {
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  let size = limitBytes;
  let unitIndex = 0;
  while (size >= 1024 && size % 1024 === 0 && unitIndex < units.length - 1) {
    size /= 1024;
    unitIndex++;
  }
  return `${size} ${units[unitIndex]}`;
}

function fallbackCodeForStatus(statusCode: number): CatalogErrorCode {
  return statusCode === 401 ? 'KB_AUTH_REQUIRED'
    : statusCode === 403 ? 'KB_AUTH_FORBIDDEN'
      : statusCode === 400 ? 'KB_RUNTIME_INPUT_INVALID'
        : statusCode === 413 ? 'KB_RUNTIME_PAYLOAD_TOO_LARGE'
          : 'KB_RUNTIME_UNEXPECTED';
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
  const parts = failureParts(payload, options.statusCode, options.limitBytes);
  const error = { ...parts.error, correlationId: parts.error.correlationId || options.correlationId };
  return createUxFailureResult(error);
}
