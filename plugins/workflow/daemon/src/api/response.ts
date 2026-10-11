import type { FastifyReply } from 'fastify';
import {
  createErrorEnvelope,
  getCatalogEntry,
  type PlatformErrorCode,
} from '@kb-labs/core-platform';
import {
  createUxFailureResult,
  createUxSuccessResult,
  type UxFailureResult,
  type UxSuccessResult,
} from '@kb-labs/core-platform/ux-result';

export type ApiSuccessResponse<T> = UxSuccessResult<T>;
export type ApiErrorResponse = UxFailureResult;
export type ApiResponse<T> = ApiSuccessResponse<T> | ApiErrorResponse;

export function ok<T>(data: T): ApiSuccessResponse<T> {
  return createUxSuccessResult(data);
}

export function fail(
  reply: FastifyReply,
  code: PlatformErrorCode,
  message: string,
  hint?: string,
  details?: Record<string, string>,
): ApiErrorResponse {
  const catalogEntry = getCatalogEntry(code);
  if (!catalogEntry?.httpStatus) {
    throw new Error(`Workflow API error code "${code}" has no catalogued HTTP status.`);
  }
  const envelope = createErrorEnvelope(code, { message, hint, details });
  reply.code(catalogEntry.httpStatus);
  return createUxFailureResult(envelope);
}
