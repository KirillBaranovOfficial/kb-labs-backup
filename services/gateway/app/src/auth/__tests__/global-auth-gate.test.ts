import { afterEach, describe, expect, it, vi } from 'vitest';
import Fastify, { type FastifyInstance } from 'fastify';
import type { ICache } from '@kb-labs/core-platform';
import type { ManifestV3 } from '@kb-labs/plugin-contracts';
import { createAuthMiddleware, isDeclaredWebhookRoute, isPlatformPublicRoute } from '../middleware.js';

function cache(): ICache {
  return {
    get: async () => null,
    set: async () => undefined,
    delete: async () => undefined,
    clear: async () => undefined,
  } as unknown as ICache;
}

describe('Gateway global auth gate', () => {
  let app: FastifyInstance | undefined;
  afterEach(async () => { await app?.close(); });

  it('rejects anonymous requests on every mounted plugin, project, and MCP route before the route runs', async () => {
    app = Fastify({ logger: false });
    app.addHook('onRequest', createAuthMiddleware(cache(), { secret: 'test-secret' }, { authEnabled: true }));
    const handler = vi.fn(async () => ({ ok: true }));
    const routeInventory = [
      { method: 'GET' as const, url: '/api/v1/studio/registry' },
      { method: 'GET' as const, url: '/api/v1/plugins/custom/health' },
      { method: 'GET' as const, url: '/api/v1/projects/:projectId/workflow/health' },
      { method: 'POST' as const, url: '/api/v1/projects/:projectId/mcp' },
      { method: 'POST' as const, url: '/api/v1/mcp' },
    ];
    for (const route of routeInventory) { app.route({ ...route, handler }); }
    await app.ready();

    const requestInventory = [
      ['GET', '/api/v1/studio/registry'],
      ['GET', '/api/v1/plugins/custom/health'],
      ['GET', '/api/v1/projects/project-1/workflow/health'],
      ['POST', '/api/v1/projects/project-1/mcp'],
      ['POST', '/api/v1/mcp'],
    ] as const;
    for (const [method, url] of requestInventory) {
      const response = await app.inject({ method, url });
      expect(response.statusCode, url).toBe(401);
      expect(response.json(), url).toMatchObject({ error: { code: 'KB_AUTH_REQUIRED', hint: 'Run "kb auth login".' } });
    }
    expect(handler).not.toHaveBeenCalled();
  });

  it('rejects invalid bearer tokens with the same envelope and does not allow webhook plugin routes implicitly', async () => {
    app = Fastify({ logger: false });
    app.addHook('onRequest', createAuthMiddleware(cache(), { secret: 'test-secret' }, { authEnabled: true }));
    const handler = vi.fn(async () => ({ ok: true }));
    app.get('/webhooks/plugin/event', handler);
    await app.ready();
    const response = await app.inject({
      method: 'GET', url: '/webhooks/plugin/event', headers: { authorization: 'Bearer invalid-token' },
    });
    expect(response.statusCode).toBe(401);
    expect(response.json()).toMatchObject({ error: { code: 'KB_AUTH_REQUIRED' } });
    expect(handler).not.toHaveBeenCalled();
  });

  it('admits only exact manifest-declared webhook routes and documented public route/method pairs', () => {
    const manifest = {
      webhooks: { handlers: [{ event: 'payment.created', handler: './handler.js#default', auth: { type: 'secret', header: 'x-hook-signature' } }] },
    } as ManifestV3;
    const declared = [{ pluginId: '@acme/payments', manifest }];
    expect(isDeclaredWebhookRoute('POST', '/webhooks/%40acme%2Fpayments/payment.created', declared)).toBe(true);
    expect(isDeclaredWebhookRoute('GET', '/webhooks/%40acme%2Fpayments/payment.created', declared)).toBe(false);
    expect(isDeclaredWebhookRoute('POST', '/webhooks/%40acme%2Fpayments/other', declared)).toBe(false);
    expect(isDeclaredWebhookRoute('POST', '/webhooks/%40acme%2Fpayments/payment.created/instance', declared)).toBe(false);
    expect(isDeclaredWebhookRoute('POST', '/webhooks/%40acme%2Fother/payment.created', declared)).toBe(false);
    expect(isPlatformPublicRoute('GET', '/studio/assets/app.js')).toBe(true);
    expect(isPlatformPublicRoute('POST', '/studio/assets/app.js')).toBe(false);
    expect(isPlatformPublicRoute('GET', '/api/v1/plugins/custom/health')).toBe(false);
  });

  it('lets the explicit login, refresh, bootstrap, health, and Studio shell routes through anonymously', async () => {
    app = Fastify({ logger: false });
    app.addHook('onRequest', createAuthMiddleware(cache(), { secret: 'test-secret' }, { authEnabled: true }));
    app.get('/health', async () => ({ ok: true }));
    app.post('/auth/login/cli', async () => ({ ok: true }));
    app.post('/auth/refresh/cli', async () => ({ ok: true }));
    app.post('/auth/activate', async () => ({ ok: true }));
    app.post('/hosts/register', async () => ({ ok: true }));
    app.get('/studio/assets/main.js', async () => 'asset');
    await app.ready();

    for (const [method, url] of [
      ['GET', '/health'], ['POST', '/auth/login/cli'], ['POST', '/auth/refresh/cli'],
      ['POST', '/auth/activate'], ['POST', '/hosts/register'], ['GET', '/studio/assets/main.js'],
    ] as const) {
      expect((await app.inject({ method, url })).statusCode, url).toBe(200);
    }
  });
});
