import type { FastifyReply } from 'fastify';
import {
  createValidatedErrorEnvelope,
  getCatalogEntry,
  type PlatformErrorCode,
  type DetailsFor,
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

type RequiredKeys<T> = { [K in keyof T]-?: {} extends Pick<T, K> ? never : K }[keyof T];
type FailureDetails<C extends PlatformErrorCode> = [RequiredKeys<DetailsFor<C>>] extends [never]
  ? [hint?: string, details?: DetailsFor<C>]
  : [hint: string | undefined, details: DetailsFor<C>];

export function ok<T>(data: T): ApiSuccessResponse<Exclude<T, undefined>> {
  if (data === undefined) throw new TypeError('Workflow API success data must be defined.');
  return createUxSuccessResult(data as Exclude<T, undefined>);
}

export function fail<C extends PlatformErrorCode>(
  reply: FastifyReply,
  code: C,
  message?: string,
  ...failureDetails: FailureDetails<C>
): ApiErrorResponse {
  const [hint, details] = failureDetails;
  const catalogEntry = getCatalogEntry(code);
  if (!catalogEntry?.httpStatus) {
    throw new Error(`Workflow API error code "${code}" has no catalogued HTTP status.`);
  }
  const envelope = createValidatedErrorEnvelope(code, { message, hint, details });
  reply.code(catalogEntry.httpStatus);
  return createUxFailureResult(envelope);
}
