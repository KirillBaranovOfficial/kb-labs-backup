import { randomUUID } from 'node:crypto';
import type { FastifyInstance } from 'fastify';
import { codeForHttpFailure, createErrorEnvelope, isErrorEnvelope, normalizeHttpFailure, statusForHttpFailure, type ILogger } from '@kb-labs/core-platform';
import { createUxSuccessResult, isUxResult } from '@kb-labs/core-platform/ux-result';
import { registerGatewayRouteSchemas } from './route-schema.js';

/** Install the shared error result at the final send boundary. */
export function registerGatewayUxErrorResponses(app: FastifyInstance, logger?: ILogger): void {
  registerGatewayRouteSchemas(app);
  app.addHook('preSerialization', async (_request, reply, payload) => {
    if (reply.statusCode >= 400) reply.serializer((value) => JSON.stringify(value));
    return payload;
  });
  app.setErrorHandler((error, request, reply) => {
    const transportStatus = error && typeof error === 'object' && 'statusCode' in error && typeof error.statusCode === 'number'
      ? error.statusCode
      : 500;
    const errorCode = codeForHttpFailure(error, transportStatus);
    const statusCode = statusForHttpFailure(error, transportStatus);
    const correlationId = String(request.id || randomUUID());
    const requestLogger = request.kbLogger ?? logger;
    requestLogger?.error('HTTP route failed', error instanceof Error ? error : new Error(String(error)), {
      correlationId, statusCode, errorCode,
    });
    const errorValue = isErrorEnvelope(error)
      ? error
      : error && typeof error === 'object' && 'code' in error
        ? { code: error.code }
        : error;
    reply.serializer((payload) => JSON.stringify(payload));
    reply.code(statusCode).send({ error: errorValue });
  });
  app.addHook('onSend', async (request, reply, payload) => {
    if (reply.statusCode < 400) return payload;
    if (payload === null || payload === undefined || Buffer.isBuffer(payload)
      || typeof (payload as { pipe?: unknown }).pipe === 'function'
      || typeof (payload as { getReader?: unknown }).getReader === 'function') return payload;
    let body: unknown = payload;
    if (typeof payload === 'string') {
      try { body = JSON.parse(payload); } catch { body = undefined; }
    }
    const result = normalizeHttpFailure(body, {
      correlationId: String(request.id || randomUUID()),
      statusCode: reply.statusCode,
    });
    reply.code(statusForHttpFailure(body, reply.statusCode));
    return JSON.stringify(result);
  });
}

/** Install the final JSON result boundary before registering route plugins. */
export function registerGatewayUxBoundary(app: FastifyInstance, logger: ILogger): void {
  registerGatewayUxErrorResponses(app, logger);
  app.setNotFoundHandler((request, reply) => {
    const route = `${request.method} ${request.url.split('?', 1)[0] ?? request.url}`;
    const error = createErrorEnvelope('KB_RUNTIME_RESOURCE_NOT_FOUND', {
      details: { route },
      message: `Gateway route ${route} was not found.`,
      correlationId: String(request.id || randomUUID()),
      hint: 'Check the Gateway route and method, then use a registered route.',
    });
    return reply.code(404).send({ error });
  });
  app.addHook('onSend', async (request, reply, payload) => {
    if (request.routeOptions.config?.ux === 'raw') return payload;
    if (reply.statusCode >= 400) return payload;
    if (typeof payload !== 'string') {
      if (payload === null || payload === undefined || Buffer.isBuffer(payload)
        || typeof (payload as { pipe?: unknown }).pipe === 'function'
        || typeof (payload as { getReader?: unknown }).getReader === 'function') return payload;
      return JSON.stringify(createUxSuccessResult(payload));
    }
    let body: unknown;
    try { body = JSON.parse(payload); } catch { return payload; }
    if (isUxResult(body)) return payload;
    return JSON.stringify(createUxSuccessResult(body));
  });
}
