import type { FastifyReply } from 'fastify';

export interface ApiSuccessResponse<T> {
  ok: true;
  data: T;
}

export interface ApiErrorResponse {
  ok: false;
  error: string;
  code?: string;
}

export type ApiResponse<T> = ApiSuccessResponse<T> | ApiErrorResponse;

export function ok<T>(data: T): ApiSuccessResponse<T> {
  return { ok: true, data };
}

export function fail(
  reply: FastifyReply,
  statusCode: number,
  message: string,
  code?: string,
): ApiErrorResponse {
  reply.code(statusCode);
  return { ok: false, error: message, ...(code ? { code } : {}) };
}
