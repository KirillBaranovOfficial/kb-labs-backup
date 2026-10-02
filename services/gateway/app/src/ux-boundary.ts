import { randomUUID } from 'node:crypto';
import type { FastifyInstance } from 'fastify';
import {
  createErrorEnvelope,
  getCatalogEntry,
  isErrorEnvelope,
  type ErrorEnvelope,
  type ILogger,
} from '@kb-labs/core-platform';
import { createUxFailureResult, createUxSuccessResult, isUxResult } from '@kb-labs/core-platform/ux-result';
import { registerGatewayRouteSchemas } from './route-schema.js';

/** Apply error schemas and normalize failures before Fastify serializes them. */
export function registerGatewayUxErrorResponses(app: FastifyInstance, logger?: ILogger): void {
  registerGatewayRouteSchemas(app);
  app.addHook('preSerialization', async (request, reply, payload) => {
    if (reply.statusCode < 400) return payload;
    if (isUxResult(payload) && !payload.ok && getCatalogEntry(payload.error.code)) {
      if (payload.error.correlationId) return payload;
      return createUxFailureResult({ ...payload.error, correlationId: String(request.id || randomUUID()) });
    }
    let body = payload;
    if (typeof body === 'string') {
      try { body = JSON.parse(body); } catch { return payload; }
    }
    const correlationId = String(request.id || randomUUID());
    const rawError = body && typeof body === 'object' && 'error' in body ? body.error : undefined;
    const typed = isErrorEnvelope(rawError) && getCatalogEntry(rawError.code) ? rawError : undefined;
    const candidateCode = typed?.code ?? (body && typeof body === 'object' && 'code' in body && typeof body.code === 'string' ? body.code : undefined);
    const code = candidateCode && getCatalogEntry(candidateCode)
      ? candidateCode
      : reply.statusCode === 401 ? 'KB_AUTH_REQUIRED'
        : reply.statusCode === 403 ? 'KB_AUTH_FORBIDDEN'
          : reply.statusCode === 400 ? 'KB_RUNTIME_INPUT_INVALID' : 'KB_RUNTIME_UNEXPECTED';
    const error: ErrorEnvelope = typed
      ? { ...typed, correlationId: typed.correlationId || correlationId }
      : createErrorEnvelope(code, { correlationId });
    return createUxFailureResult(error);
  });
  app.setErrorHandler((error, request, reply) => {
    const typedError = error instanceof Error ? error as Error & { statusCode?: number; code?: string } : undefined;
    const statusCode = typeof typedError?.statusCode === 'number' ? typedError.statusCode : 500;
    const correlationId = String(request.id || randomUUID());
    const candidateCode = typeof typedError?.code === 'string' && getCatalogEntry(typedError.code) ? typedError.code : undefined;
    const code = candidateCode ?? (statusCode === 401 ? 'KB_AUTH_REQUIRED' : statusCode === 403 ? 'KB_AUTH_FORBIDDEN' : statusCode === 400 ? 'KB_RUNTIME_INPUT_INVALID' : 'KB_RUNTIME_UNEXPECTED');
    const envelope: ErrorEnvelope = createErrorEnvelope(code, { correlationId });
    const requestLogger = request.kbLogger ?? logger;
    requestLogger?.error('HTTP route failed', typedError ?? new Error(String(error)), { correlationId, statusCode, errorCode: code });
    reply.code(statusCode).send(createUxFailureResult(envelope));
  });
}

/** Install the final JSON result boundary before registering route plugins. */
export function registerGatewayUxBoundary(app: FastifyInstance, logger: ILogger): void {
  registerGatewayUxErrorResponses(app, logger);
  app.addHook('onSend', async (request, reply, payload) => {
    if (request.routeOptions.config.ux === 'raw' && reply.statusCode < 400) return payload;
    if (typeof payload !== 'string') return payload;
    let body: unknown;
    try { body = JSON.parse(payload); } catch { return payload; }
    if (reply.statusCode >= 400 || isUxResult(body)) return payload;
    return JSON.stringify(createUxSuccessResult(body));
  });
}
