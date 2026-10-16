import Fastify from 'fastify';
import { describe, expect, it } from 'vitest';
import { createErrorEnvelope, serializeError } from '@kb-labs/core-platform';
import { mountRoutes } from '../route-mounter.js';
import type { ExecutionBackend, ExecutionResult } from '../../types.js';

function backendReturning(result: ExecutionResult): ExecutionBackend {
  return {
    async execute() { return result as unknown as Awaited<ReturnType<ExecutionBackend['execute']>>; },
    async health() { return { healthy: true, backend: 'in-process', executionMode: 'in-process' }; },
    async stats() { return { totalExecutions: 0, successCount: 0, errorCount: 0, avgExecutionTimeMs: 0 }; },
    async shutdown() {},
    executionMode: 'in-process',
  };
}

const manifest = {
  schema: 'kb.plugin/3' as const,
  id: '@kb-labs/viewer',
  version: '0.1.0',
  rest: { routes: [{ method: 'GET' as const, path: '/index', handler: './index.js#default' }] },
};
const options = { pluginRoot: '/plugin', workspaceRoot: '/workspace' };

// case: S-203.1
it('wraps plugin REST success payloads in the shared UX result envelope', async () => {
  const server = Fastify();
  await mountRoutes(server, manifest, { ...options, backend: backendReturning({ ok: true, data: { title: 'Architecture' }, executionTimeMs: 1 }) });

  const response = await server.inject({ method: 'GET', url: '/index' });
  expect(response.statusCode).toBe(200);
  expect(response.json()).toEqual({ ok: true, data: { title: 'Architecture' } });
  await server.close();
});

// case: S-203.2
it('returns a shared safe error envelope for plugin REST failures', async () => {
  const server = Fastify();
  const envelope = createErrorEnvelope('KB_RUNTIME_INPUT_INVALID', {
    message: 'The request is invalid.',
    hint: 'Correct the request and retry.',
    correlationId: 'viewer-test-request',
  });
  await mountRoutes(server, manifest, { ...options, backend: backendReturning({
    ok: false,
    error: { ...envelope, error: serializeError(new Error('The request is invalid.')) },
    executionTimeMs: 1,
  }) });

  const response = await server.inject({ method: 'GET', url: '/index', headers: { 'x-request-id': 'viewer-test-request' } });
  const body = response.json();
  expect(response.statusCode).toBe(400);
  expect(body).toMatchObject({ ok: false, error: { code: 'KB_RUNTIME_INPUT_INVALID', correlationId: 'viewer-test-request' } });
  expect(response.body).not.toContain('secret stack detail');
  await server.close();
});
