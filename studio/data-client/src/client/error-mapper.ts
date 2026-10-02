import { KBError, errorCodes } from '../errors/kb-error';
import type { ResponseError } from './types';
import type { ErrorEnvelope } from '@kb-labs/rest-api-contracts';
import { ulid } from 'ulid';

/**
 * Error envelope structure
 */
interface ErrorEnvelopeResponse {
  ok: false;
  error: {
    code: string;
    message: string;
    details?: Record<string, unknown>;
    cause?: string;
    traceId?: string;
  };
  meta?: {
    requestId: string;
    durationMs: number;
    apiVersion: string;
  };
}

/**
 * Type guard for ErrorEnvelopeResponse
 */
function isErrorEnvelopeResponse(obj: unknown): obj is ErrorEnvelopeResponse {
  return (
    typeof obj === 'object' &&
    obj !== null &&
    'ok' in obj &&
    (obj as { ok: unknown }).ok === false &&
    'error' in obj &&
    typeof (obj as { error: unknown }).error === 'object' &&
    (obj as { error: unknown }).error !== null &&
    'message' in (obj as { error: object }).error
  );
}

export function mapFetchError(error: unknown, response?: Response): KBError {
  if (!response) {
    if (error instanceof KBError) {
      return error;
    }
    if (error instanceof TypeError && error.message.includes('fetch')) {
      return new KBError(errorCodes.NETWORK_ERROR, 'Network request failed', undefined, error);
    }
    return new KBError(errorCodes.NETWORK_ERROR, 'Unknown error occurred', undefined, error);
  }

  const parsedBody = error;
  if (isSharedFailureEnvelope(parsedBody)) {
    return new KBError(
      parsedBody.error.code,
      parsedBody.error.message,
      response.status,
      undefined,
      parsedBody.error.hint,
      parsedBody.error.correlationId,
    );
  }

  if (isFailureEnvelopeCandidate(parsedBody)) {
    const details = parsedBody.error;
    return new KBError(
      'KB_RUNTIME_UNEXPECTED',
      'The operation failed unexpectedly.',
      response.status,
      undefined,
      'Copy diagnostics and contact support.',
      typeof details.correlationId === 'string' ? details.correlationId : undefined,
    );
  }

  const errorEnvelope =
    parsedBody && typeof parsedBody === 'object' && isErrorEnvelopeResponse(parsedBody)
      ? (parsedBody as ErrorEnvelopeResponse)
      : null;
  const httpError: ResponseError = {
    status: response.status,
    statusText: response.statusText,
    data: parsedBody ?? null,
  };

  // Map error codes from api-contracts
  const statusCode = response.status;
  let errorMessage: string | undefined = undefined;
  if (errorEnvelope !== null) {
    const envelope: ErrorEnvelopeResponse = errorEnvelope;
    errorMessage = envelope.error.message;
  }
  
  if (statusCode === 404) {
    return new KBError(errorCodes.NOT_FOUND, errorMessage || 'Resource not found', 404, httpError);
  }
  if (statusCode === 401) {
    return new KBError(errorCodes.AUTH_ERROR, errorMessage || 'Authentication failed', 401, httpError);
  }
  if (statusCode === 403) {
    return new KBError(errorCodes.AUTH_ERROR, errorMessage || 'Forbidden', 403, httpError);
  }
  if (statusCode === 409) {
    return new KBError(errorCodes.CONFLICT, errorMessage || 'Conflict', 409, httpError);
  }
  if (statusCode === 429) {
    return new KBError(errorCodes.RATE_LIMIT, errorMessage || 'Rate limit exceeded', 429, httpError);
  }
  if (statusCode >= 500) {
    return new KBError(errorCodes.SERVER_ERROR, errorMessage || 'Server error', statusCode, httpError);
  }
  return new KBError(errorCodes.NETWORK_ERROR, errorMessage || httpError.statusText || 'Request failed', statusCode, httpError);
}

function isSharedFailureEnvelope(value: unknown): value is {
  ok: false;
  error: { code: string; message: string; hint: string; correlationId?: string };
} {
  if (!isFailureEnvelopeCandidate(value)) { return false; }
  const error = value.error;
  return Object.keys(value).every((key) => key === 'ok' || key === 'error') &&
    typeof error.code === 'string' && /^[A-Z][A-Z0-9]*(_[A-Z0-9]+)+$/.test(error.code) &&
    typeof error.message === 'string' && error.message.length > 0 &&
    typeof error.area === 'string' && typeof error.stage === 'string' &&
    (error.severity === 'error' || error.severity === 'warning') &&
    typeof error.retryable === 'boolean' && typeof error.hint === 'string' && error.hint.length > 0;
}

function isFailureEnvelopeCandidate(value: unknown): value is {
  ok: false;
  error: Record<string, unknown>;
} {
  return typeof value === 'object' && value !== null &&
    'ok' in value && value.ok === false &&
    'error' in value && typeof value.error === 'object' && value.error !== null;
}

export function mapInvalidResultEnvelope(value: unknown, status: number): KBError {
  const nestedError = isRecord(value) && isRecord(value.error) ? value.error : undefined;
  return new KBError(
    'KB_RUNTIME_UNEXPECTED',
    'The operation failed unexpectedly.',
    status,
    undefined,
    'Copy diagnostics and contact support.',
    typeof nestedError?.correlationId === 'string' ? nestedError.correlationId : `studio-${ulid()}`,
  );
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/**
 * Map error envelope to KBError
 */
export function mapErrorEnvelope(envelope: ErrorEnvelope): KBError {
  const httpError: ResponseError = {
    status: 400, // Default status
    statusText: envelope.error.message,
    data: envelope,
  };

  // Map error code from api-contracts to KBError codes
  const errorCode = envelope.error.code;
  
  if (errorCode === 'E_NOT_FOUND') {
    return new KBError(errorCodes.NOT_FOUND, envelope.error.message, 404, httpError);
  }
  if (errorCode === 'E_UNAUTHORIZED') {
    return new KBError(errorCodes.AUTH_ERROR, envelope.error.message, 401, httpError);
  }
  if (errorCode === 'E_FORBIDDEN') {
    return new KBError(errorCodes.AUTH_ERROR, envelope.error.message, 403, httpError);
  }
  if (errorCode === 'E_CONFLICT') {
    return new KBError(errorCodes.CONFLICT, envelope.error.message, 409, httpError);
  }
  if (errorCode === 'E_RATE_LIMIT') {
    return new KBError(errorCodes.RATE_LIMIT, envelope.error.message, 429, httpError);
  }
  if (errorCode === 'E_TIMEOUT') {
    return new KBError(errorCodes.TIMEOUT, envelope.error.message, 408, httpError);
  }
  if (errorCode.startsWith('E_TOOL_')) {
    return new KBError(errorCodes.TOOL_ERROR, envelope.error.message, 500, httpError);
  }

  return new KBError(errorCodes.SERVER_ERROR, envelope.error.message, 500, httpError);
}
