/**
 * @module @kb-labs/rest-api-app/middleware/envelope
 * Response envelope wrapper middleware — single source of truth for error formatting.
 */

import type { FastifyInstance } from 'fastify';
import type { RestApiConfig } from '@kb-labs/rest-api-core';
import { createErrorEnvelope, getCatalogEntry } from '@kb-labs/core-platform';
import { isErrorEnvelope, type ErrorEnvelope } from '@kb-labs/core-platform/error-envelope';
import { createUxFailureResult, createUxSuccessResult, isUxResult } from '@kb-labs/core-platform/ux-result';
import type { PluginErrorEnvelope } from '@kb-labs/rest-api-contracts';

function isPluginErrorEnvelope(err: unknown): err is PluginErrorEnvelope {
  return (
    err !== null &&
    typeof err === 'object' &&
    'status' in err &&
    (err as PluginErrorEnvelope).status === 'error' &&
    'meta' in err &&
    typeof (err as PluginErrorEnvelope).meta === 'object' &&
    'pluginId' in (err as PluginErrorEnvelope).meta
  );
}

/**
 * Register envelope middleware
 */
export function registerEnvelopeMiddleware<Server extends FastifyInstance>(
  server: Server,
  config: RestApiConfig
): void {
  server.addHook('preSerialization', async (request, reply, payload) => {
    if (reply.statusCode < 400) return payload;

    const correlationId = String(request.id || '');
    const rawError = payload && typeof payload === 'object' && 'error' in payload
      ? payload.error
      : undefined;
    const typedError = isErrorEnvelope(rawError) ? rawError : undefined;
    const rawCode = rawError && typeof rawError === 'object' && 'code' in rawError
      ? rawError.code
      : undefined;
    const knownCode = typeof rawCode === 'string' && getCatalogEntry(rawCode) ? rawCode : undefined;
    const code = typedError && getCatalogEntry(typedError.code)
      ? typedError.code
      : knownCode
        ? knownCode
      : reply.statusCode === 401 ? 'KB_AUTH_REQUIRED'
        : reply.statusCode === 403 ? 'KB_AUTH_FORBIDDEN'
          : reply.statusCode === 400 ? 'KB_RUNTIME_INPUT_INVALID' : 'KB_RUNTIME_UNEXPECTED';
    const error = typedError && getCatalogEntry(typedError.code)
      ? { ...typedError, correlationId: typedError.correlationId || correlationId }
      : createErrorEnvelope(code, { correlationId });

    return createUxFailureResult(error);
  });

  // Add response schema version header and wrap in envelope
  // Use 'onSend' hook to modify payload after Fastify serializes it but before sending
  // This ensures we get the serialized JSON string and can wrap it properly
  server.addHook('onSend', async (request, reply, payload) => {
    if (request.routeOptions.config.ux === 'raw' && reply.statusCode < 400) return payload;

    // Set header with try-catch to prevent ERR_HTTP_HEADERS_SENT
    try {
      if (!reply.raw.headersSent) {
        reply.header('x-schema-version', config.apiVersion);
      }
    } catch (err) {
      // Headers already sent, ignore
    }

    // Serialize plain objects that reached onSend without going through
    // Fastify's serializer (happens with non-JSON content-type or in error
    // handler fallback chains). Buffer/stream/null pass through unchanged.
    if (typeof payload !== 'string') {
      if (
        payload === null ||
        payload === undefined ||
        Buffer.isBuffer(payload) ||
        typeof (payload as { pipe?: unknown }).pipe === 'function' ||
        typeof (payload as { getReader?: unknown }).getReader === 'function'
      ) {
        return payload;
      }
      // Plain object — serialize so onSendEnd receives a string
      try {
        return JSON.stringify(payload);
      } catch {
        return payload;
      }
    }

    // Parse the serialized JSON payload
    let parsedPayload: unknown;
    try {
      parsedPayload = JSON.parse(payload);
    } catch {
      // If not JSON, return as is
      return payload;
    }

    // Pass through a valid UX result on a successful HTTP response.
    if (reply.statusCode < 400 && isUxResult(parsedPayload)) {
      return payload;
    }

    if (reply.statusCode >= 400) {
      const correlationId = String(request.id || '');
      const rawError = parsedPayload && typeof parsedPayload === 'object' && 'error' in parsedPayload
        ? parsedPayload.error
        : undefined;
      const typedError = isErrorEnvelope(rawError) ? rawError : undefined;
      const code = typedError && getCatalogEntry(typedError.code)
        ? typedError.code
        : reply.statusCode === 401 ? 'KB_AUTH_REQUIRED'
          : reply.statusCode === 403 ? 'KB_AUTH_FORBIDDEN'
            : reply.statusCode === 400 ? 'KB_RUNTIME_INPUT_INVALID' : 'KB_RUNTIME_UNEXPECTED';
      const error = typedError && getCatalogEntry(typedError.code)
        ? { ...typedError, correlationId: typedError.correlationId || correlationId }
        : createErrorEnvelope(code, { correlationId });
      return JSON.stringify(createUxFailureResult(error));
    }

    // Skip raw OpenAPI spec — marked by x-openapi-spec header set in shared-http
    if (reply.getHeader('x-openapi-spec')) {
      return payload;
    }

    // Wrap all successful non-streaming responses in the shared envelope.

    // Handle empty payloads
    let dataToWrap = parsedPayload;

    // If payload is undefined or null, wrap as null
    if (parsedPayload === null || parsedPayload === undefined) {
      dataToWrap = null;
    }

    // Wrap successful route payloads at the final response boundary.
    const envelope = createUxSuccessResult(dataToWrap, {
        requestId: request.id || '',
        durationMs: reply.elapsedTime || 0,
        apiVersion: config.apiVersion,
      });

    // Set Content-Type header if not set
    if (!reply.getHeader('content-type')) {
      try {
        if (!reply.raw.headersSent) {
          reply.header('content-type', 'application/json');
        }
      } catch (err) {
        // Headers already sent, ignore
      }
    }

    // Serialize and return envelope as string
    return JSON.stringify(envelope);
  });

  // Single error handler: normalises both PluginErrorEnvelope (from plugin runtime)
  // and plain Errors into the canonical ErrorEnvelope { ok: false, error, meta }.
  server.setErrorHandler(async (error: unknown, request, reply) => {
    let statusCode: number;
    let errorCode: string | undefined;

    if (isPluginErrorEnvelope(error)) {
      statusCode = error.http || 500;
      errorCode = error.code;
    } else {
      const err = error instanceof Error ? error as Error & { statusCode?: number; code?: string } : undefined;
      statusCode = err?.statusCode || 500;
      errorCode = err?.code;
    }

    const correlationId = String(request.id || '');
    const code = errorCode && getCatalogEntry(errorCode)
      ? errorCode
      : statusCode === 401 ? 'KB_AUTH_REQUIRED'
        : statusCode === 403 ? 'KB_AUTH_FORBIDDEN'
          : statusCode === 400 ? 'KB_RUNTIME_INPUT_INVALID' : 'KB_RUNTIME_UNEXPECTED';
    reply.errorCode = code;

    if (request.kbLogger) {
      request.kbLogger.error('Request error', error instanceof Error ? error : new Error(String(error)), {
        errorCode: code,
        statusCode,
        correlationId,
      });
    }

    const errorEnvelope: ErrorEnvelope = createErrorEnvelope(code, { correlationId });

    reply.status(statusCode).send(createUxFailureResult(errorEnvelope));
  });
}
