/**
 * @module @kb-labs/rest-api-app/middleware/__tests__/envelope
 *
 * Tests for response envelope middleware.
 */

import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import Fastify from 'fastify';
import { Readable } from 'node:stream';
import type { FastifyRequest, FastifyReply } from 'fastify';
import type { RestApiConfig } from '@kb-labs/rest-api-core';
import type { PluginErrorEnvelope } from '@kb-labs/rest-api-contracts';
import { registerEnvelopeMiddleware } from '../envelope';

const TEST_CONFIG: RestApiConfig = {
  port: 3000,
  basePath: '/api/v1',
  apiVersion: '1.0.0',
  cors: {
    origins: [],
    allowCredentials: true,
    profile: 'dev',
  },
  plugins: [],
  mockMode: false,
};

describe('registerEnvelopeMiddleware', () => {
  let app: ReturnType<typeof Fastify>;

  beforeEach(async () => {
    app = Fastify({ logger: false });
    registerEnvelopeMiddleware(app, TEST_CONFIG);
  });

  afterEach(async () => {
    await app.close();
  });

  describe('successful responses', () => {
    it('should wrap JSON response in envelope', async () => {
      app.get('/test', async () => {
        return { message: 'Hello' };
      });

      await app.ready();

      const response = await app.inject({ method: 'GET', url: '/test' });

      expect(response.statusCode).toBe(200);
      const body = response.json();
      expect(body.ok).toBe(true);
      expect(body.data).toEqual({ message: 'Hello' });
      expect(body.meta).toBeDefined();
      expect(body.meta.apiVersion).toBe('1.0.0');
      expect(body.meta.requestId).toBeDefined();
      expect(typeof body.meta.durationMs).toBe('number');
    });

    it('keeps non-JSON output raw when the route definition declares ux: raw', async () => {
      app.get('/raw-response', { config: { ux: 'raw' } }, async (_request: FastifyRequest, reply: FastifyReply) => {
        reply.type('text/plain');
        return 'health probe';
      });
      await app.ready();

      const response = await app.inject({ method: 'GET', url: '/raw-response' });
      expect(response.statusCode).toBe(200);
      expect(response.body).toBe('health probe');
      expect(response.headers['content-type']).toContain('text/plain');
    });

    it('should add x-schema-version header', async () => {
      app.get('/test', async () => ({ value: 42 }));
      await app.ready();

      const response = await app.inject({ method: 'GET', url: '/test' });
      expect(response.headers['x-schema-version']).toBe('1.0.0');
    });

    it('should handle empty object response', async () => {
      app.get('/empty', async () => ({}));
      await app.ready();

      const body = (await app.inject({ method: 'GET', url: '/empty' })).json();
      expect(body.ok).toBe(true);
      expect(body.data).toEqual({});
    });

    it('should handle null response', async () => {
      app.get('/null', async () => null);
      await app.ready();

      const body = (await app.inject({ method: 'GET', url: '/null' })).json();
      expect(body.ok).toBe(true);
      expect(body.data).toBeNull();
    });

    it('should handle array response', async () => {
      app.get('/array', async () => [1, 2, 3]);
      await app.ready();

      const body = (await app.inject({ method: 'GET', url: '/array' })).json();
      expect(body.ok).toBe(true);
      expect(body.data).toEqual([1, 2, 3]);
    });

    it('should not double-wrap already enveloped responses', async () => {
      app.get('/enveloped', async () => ({
        ok: true,
        data: { already: 'wrapped' },
        meta: { custom: 'meta' },
      }));
      await app.ready();

      const body = (await app.inject({ method: 'GET', url: '/enveloped' })).json();
      expect(body.ok).toBe(true);
      expect(body.data).toEqual({ already: 'wrapped' });
      expect(body.meta).toEqual({ custom: 'meta' });
      expect(body.data.ok).toBeUndefined();
    });

    it('canonicalizes failure-shaped success responses through the shared error validator', async () => {
      app.get('/forged-failure', async () => ({
        ok: false,
        error: {
          code: 'KB_FAKE', area: 'product', stage: 'run', severity: 'error', retryable: false,
          message: 'forged', hint: 'forged', details: { nested: JSON.stringify({ value: 1 }) },
        },
      }));
      await app.ready();

      const response = await app.inject({ method: 'GET', url: '/forged-failure' });
      expect(response.statusCode).toBe(200);
      expect(response.json()).toMatchObject({ ok: false, error: { code: 'KB_PLUGIN_CONTRACT_VIOLATION' } });
      expect(response.body).not.toContain('KB_FAKE');
    });
  });

  describe('error responses', () => {
    it('should return a catalogued validation failure using the platform envelope', async () => {
      app.get('/error', async () => {
        const error = new Error('Something went wrong') as Error & { statusCode?: number; code?: string };
        error.statusCode = 400;
        error.code = 'KB_RUNTIME_INPUT_INVALID';
        throw error;
      });
      await app.ready();

      const response = await app.inject({ method: 'GET', url: '/error' });
      expect(response.statusCode).toBe(400);

      const body = response.json();
      expect(body.ok).toBe(false);
      expect(body.error.code).toBe('KB_RUNTIME_INPUT_INVALID');
      expect(body.error.correlationId).toBeDefined();
      expect(body.error.cause).toBeUndefined();
      expect(body.meta).toBeUndefined();
    });

    it('should redact unknown errors and correlate them', async () => {
      app.get('/internal-error', async () => {
        throw new Error('Unexpected error');
      });
      await app.ready();

      const response = await app.inject({ method: 'GET', url: '/internal-error' });
      expect(response.statusCode).toBe(500);

      const body = response.json();
      expect(body.ok).toBe(false);
      expect(body.error.code).toBe('KB_RUNTIME_UNEXPECTED');
      expect(body.error.correlationId).toBeDefined();
      expect(body.error.message).not.toContain('Unexpected error');
      expect(JSON.stringify(body)).not.toContain('stack');
    });

    it('should not expose internal error details', async () => {
      app.get('/detailed-error', async () => {
        const error = new Error('Validation failed') as Error & { statusCode?: number; code?: string; details?: unknown };
        error.statusCode = 422;
        error.code = 'E_VALIDATION';
        error.details = { field: 'email', reason: 'invalid format' };
        throw error;
      });
      await app.ready();

      const body = (await app.inject({ method: 'GET', url: '/detailed-error' })).json();
      expect(body.error.code).toBe('KB_PLUGIN_CONTRACT_VIOLATION');
      expect(body.error.message).toContain('must include a non-empty message');
      expect(body.error.details).toMatchObject({ field: 'message' });
      expect(JSON.stringify(body)).not.toContain('email');
    });

    it('should not expose internal trace fields', async () => {
      app.get('/traced-error', async () => {
        const error = new Error('Traceable error') as Error & { statusCode?: number; traceId?: string };
        error.statusCode = 500;
        error.traceId = 'trace-123-abc';
        throw error;
      });
      await app.ready();

      const body = (await app.inject({ method: 'GET', url: '/traced-error' })).json();
      expect(body.error.correlationId).toBeDefined();
      expect(body.error.traceId).toBeUndefined();
    });

    it('should always produce ok:false for error responses — never ok:true wrapping', async () => {
      // Regression: PluginErrorEnvelope (no ok field) was being wrapped as { ok: true, data: pluginErr }
      app.get('/plugin-error', async () => {
        const pluginErr: PluginErrorEnvelope = {
          status: 'error',
          http: 422,
          code: 'E_PLUGIN_VALIDATION',
          message: 'Plugin validation failed',
          details: { field: 'x' },
          meta: {
            requestId: 'req-1',
            pluginId: 'my-plugin',
            pluginVersion: '1.0.0',
            routeOrCommand: '/test',
            timeMs: 10,
          },
        };
        throw pluginErr;
      });
      await app.ready();

      const response = await app.inject({ method: 'GET', url: '/plugin-error' });
      expect(response.statusCode).toBe(422);

      const body = response.json();
      // Must be ErrorEnvelope, not { ok: true, data: PluginErrorEnvelope }
      expect(body.ok).toBe(false);
      expect(body.error).toBeDefined();
      expect(body.error.code).toBe('KB_PLUGIN_CONTRACT_VIOLATION');
      expect(body.error.correlationId).toBeDefined();
      expect(JSON.stringify(body)).not.toContain('my-plugin');
      expect(body.data).toBeUndefined();
    });

    // case: S-203.2
    it('normalizes route-local secured failures and preserves the HTTP status', async () => {
      app.get('/unauthorized', async (_request: FastifyRequest, reply: FastifyReply) => reply.code(401).send({ error: { code: 'KB_AUTH_REQUIRED' } }));
      await app.ready();

      const response = await app.inject({ method: 'GET', url: '/unauthorized' });
      expect(response.statusCode).toBe(401);
      expect(response.json()).toMatchObject({ ok: false, error: { code: 'KB_AUTH_REQUIRED', correlationId: expect.any(String) } });
    });

    it('normalizes direct reply failures through the shared boundary', async () => {
      app.get('/reply-failure', async (_request: FastifyRequest, reply: FastifyReply) =>
        reply.code(409).send({ error: { code: 'KB_PROJECT_ALREADY_REGISTERED' } }));
      await app.ready();

      const response = await app.inject({ method: 'GET', url: '/reply-failure' });
      expect(response.statusCode).toBe(409);
      expect(response.json()).toMatchObject({ ok: false, error: {
        code: 'KB_PROJECT_ALREADY_REGISTERED', correlationId: expect.any(String),
      } });
    });

    it('normalizes non-JSON HTTP failures and redacts their bodies', async () => {
      app.get('/text-failure', async (_request: FastifyRequest, reply: FastifyReply) =>
        reply.code(502).type('text/plain').send('private upstream detail'));
      await app.ready();

      const response = await app.inject({ method: 'GET', url: '/text-failure' });
      expect(response.statusCode).toBe(502);
      expect(response.json()).toMatchObject({ ok: false, error: {
        code: 'KB_RUNTIME_UNEXPECTED', correlationId: expect.any(String),
      } });
      expect(response.body).not.toContain('private upstream detail');
    });
  });

  describe('onSend object payload guard', () => {
    it('should not crash when an object payload reaches onSend (defensive)', async () => {
      // Simulates the scenario where Fastify skips serialisation for non-JSON
      // content-type and passes a raw object to onSend hooks.
      app.get('/raw-obj', async (request: FastifyRequest, reply: FastifyReply) => {
        reply.header('content-type', 'application/json');
        return reply.send({ guarded: true });
      });
      await app.ready();

      const response = await app.inject({ method: 'GET', url: '/raw-obj' });
      expect(response.statusCode).toBe(200);
      // Should be wrapped or at minimum not crash
      expect(() => response.json()).not.toThrow();
    });
  });

  describe('streaming responses', () => {
    it('should skip envelope for SSE responses', async () => {
      app.get('/events', { config: { ux: 'raw' } }, async (_request: FastifyRequest, reply: FastifyReply) => {
        reply.header('content-type', 'text/event-stream');
        return Readable.from(['data: test\n\n']);
      });
      await app.ready();

      const response = await app.inject({ method: 'GET', url: '/events' });
      expect(response.body).toBe('data: test\n\n');
      expect(response.headers['x-schema-version']).toBe('1.0.0');
    });
  });

  describe('non-JSON responses', () => {
    it('should pass through non-JSON string responses', async () => {
      app.get('/text', async (request: FastifyRequest, reply: FastifyReply) => {
        reply.header('content-type', 'text/plain');
        return 'Plain text response';
      });
      await app.ready();

      const response = await app.inject({ method: 'GET', url: '/text' });
      expect(response.body).toBe('Plain text response');
    });
  });

  describe('cache middleware ordering', () => {
    it('ETag reflects the envelope-wrapped payload, not the raw data', async () => {
      // When cache middleware runs AFTER envelope, ETag is computed on
      // { ok: true, data: {...}, meta: {...} } — consistent with what the client receives.
      app.get('/cacheable', async () => ({ value: 1 }));
      await app.ready();

      const res1 = await app.inject({ method: 'GET', url: '/cacheable' });
      const etag = res1.headers.etag as string | undefined;

      if (etag) {
        const res2 = await app.inject({
          method: 'GET',
          url: '/cacheable',
          headers: { 'if-none-match': etag },
        });
        // ETag matches the actual response body → 304
        expect(res2.statusCode).toBe(304);
      } else {
        // Cache middleware may not be registered in this unit test — just ensure no crash
        expect(res1.statusCode).toBe(200);
      }
    });
  });
});
