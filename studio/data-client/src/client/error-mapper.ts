import { KBError, errorCodes } from '../errors/kb-error';
import type { ResponseError } from './types';
import { isUxResult } from '@kb-labs/core-platform/ux-result';
import type { ErrorEnvelope as RestApiErrorEnvelope } from '@kb-labs/rest-api-contracts';
import { ulid } from 'ulid';

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
  if (isUxResult(parsedBody) && !parsedBody.ok) {
    return new KBError(
      parsedBody.error.code,
      parsedBody.error.message,
      response.status,
      undefined,
      parsedBody.error.hint,
      parsedBody.error.correlationId,
    );
  }

  const httpError: ResponseError = {
    status: response.status,
    statusText: response.statusText,
    data: parsedBody ?? null,
  };

  // Preserve status semantics for responses outside the shared result contract.
  const statusCode = response.status;

  if (statusCode === 404) {
    return new KBError(errorCodes.NOT_FOUND, 'Resource not found', 404, httpError);
  }
  if (statusCode === 401) {
    return new KBError(errorCodes.AUTH_ERROR, 'Authentication failed', 401, httpError);
  }
  if (statusCode === 403) {
    return new KBError(errorCodes.AUTH_ERROR, 'Forbidden', 403, httpError);
  }
  if (statusCode === 409) {
    return new KBError(errorCodes.CONFLICT, 'Conflict', 409, httpError);
  }
  if (statusCode === 429) {
    return new KBError(errorCodes.RATE_LIMIT, 'Rate limit exceeded', 429, httpError);
  }
  if (statusCode >= 500) {
    return new KBError(errorCodes.SERVER_ERROR, 'Server error', statusCode, httpError);
  }
  return new KBError(errorCodes.NETWORK_ERROR, httpError.statusText || 'Request failed', statusCode, httpError);
}

export function mapInvalidResultEnvelope(status: number): KBError {
  return new KBError(
    'KB_RUNTIME_UNEXPECTED',
    'The operation failed unexpectedly.',
    status,
    undefined,
    'Copy diagnostics and contact support.',
    `studio-${ulid()}`,
  );
}

/**
 * Map error envelope to KBError
 */
export function mapErrorEnvelope(envelope: RestApiErrorEnvelope): KBError {
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
