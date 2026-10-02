import Fastify from 'fastify';
import { createErrorEnvelope, type ILogger } from '@kb-labs/core-platform';
import { createUxFailureResult, isUxResult } from '@kb-labs/core-platform/ux-result';
import successFixture from '../../../../../contracts/ux-result/fixtures/valid/success.json';
import failureFixture from '../../../../../contracts/ux-result/fixtures/valid/failure.json';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { registerGatewayUxBoundary } from '../ux-boundary.js';

describe('Gateway UX boundary', () => {
  const apps: ReturnType<typeof Fastify>[] = [];

  afterEach(async () => {
    await Promise.all(apps.splice(0).map(app => app.close()));
  });

  // case: S-203.1, S-203.2, S-203.3, S-203.6
  it('covers each route discovered from the live Fastify registry', async () => {
    const app = Fastify({ logger: false });
    apps.push(app);
    const errorLog = vi.fn();
    const logger = { info: vi.fn(), warn: vi.fn(), error: errorLog, debug: vi.fn(), child: vi.fn() } as unknown as ILogger;
    const routes: Array<{ method: string | string[]; url: string }> = [];
    app.addHook('onRoute', route => { routes.push({ method: route.method, url: route.url }); });
    registerGatewayUxBoundary(app, logger);
    app.get('/contract-probe', async (request, reply) => {
      const mode = (request.query as { mode?: string }).mode;
      if (mode === 'typed') return reply.code(422).send(createUxFailureResult(createErrorEnvelope('KB_RUNTIME_INPUT_INVALID')));
      if (mode === 'unexpected') throw new Error('private stack detail');
      return { accepted: true };
    });
    await app.ready();

    expect(isUxResult(successFixture)).toBe(true);
    expect(isUxResult(failureFixture)).toBe(true);
    expect(routes.length).toBeGreaterThan(0);
    for (const route of routes) {
      const registeredMethod = Array.isArray(route.method) ? route.method[0] : route.method;
      expect(['GET', 'HEAD']).toContain(registeredMethod);
      const success = await app.inject({ method: 'GET', url: `${route.url}?mode=success` });
      expect(success.statusCode).toBe(200);
      expect(isUxResult(success.json())).toBe(true);
      expect(success.json()).toMatchObject({ ok: true, data: { accepted: true } });

      const typed = await app.inject({ method: 'GET', url: `${route.url}?mode=typed` });
      expect(typed.statusCode).toBe(422);
      expect(isUxResult(typed.json())).toBe(true);
      expect(typed.json().error.code).toBe('KB_RUNTIME_INPUT_INVALID');

      const unexpected = await app.inject({ method: 'GET', url: `${route.url}?mode=unexpected` });
      expect(unexpected.statusCode).toBe(500);
      expect(isUxResult(unexpected.json())).toBe(true);
      expect(unexpected.json().error).toMatchObject({ code: 'KB_RUNTIME_UNEXPECTED', correlationId: expect.any(String) });
      expect(unexpected.body).not.toContain('private stack detail');
      expect(errorLog).toHaveBeenCalledWith('HTTP route failed', expect.any(Error), expect.objectContaining({ correlationId: unexpected.json().error.correlationId }));
    }
  });
});
