import Fastify from 'fastify';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createRegistryAuthMiddleware } from '../auth-middleware.js';

describe('registry gateway identity', () => {
  let app: Awaited<ReturnType<typeof Fastify>> | undefined;
  afterEach(async () => { await app?.close(); });

  it('rejects direct or anonymous requests without the gateway secret', async () => {
    app = Fastify();
    app.addHook('onRequest', createRegistryAuthMiddleware('registry-internal-secret'));
    const handler = vi.fn(async (request: { authContext?: unknown }) => ({ auth: request.authContext ?? null }));
    app.get('/api/v1/my/packages', handler);
    await app.ready();

    for (const headers of [{}, { 'x-internal-secret': 'wrong' }]) {
      const response = await app.inject({ method: 'GET', url: '/api/v1/my/packages', headers });
      expect(response.statusCode).toBe(401);
      expect(response.json()).toMatchObject({ error: { code: 'KB_AUTH_REQUIRED' } });
    }
    expect(handler).not.toHaveBeenCalled();
  });

  it('accepts only gateway-authenticated identity and does not require a user bearer token', async () => {
    app = Fastify();
    app.addHook('onRequest', createRegistryAuthMiddleware('registry-internal-secret'));
    app.get('/api/v1/my/packages', async (request: { authContext?: unknown }) => ({ auth: request.authContext }));
    await app.ready();

    const response = await app.inject({
      method: 'GET',
      url: '/api/v1/my/packages',
      headers: {
        'x-internal-secret': 'registry-internal-secret',
        'x-kb-auth-type': 'user',
        'x-kb-auth-user-id': 'member-1',
        'x-kb-auth-namespace-id': 'tenant-1',
        'x-kb-auth-tier': 'free',
        'x-kb-auth-permissions': '["packages:read"]',
      },
    });
    expect(response.statusCode).toBe(200);
    expect(response.json()).toMatchObject({
      auth: { type: 'user', userId: 'member-1', namespaceId: 'tenant-1', permissions: ['packages:read'] },
    });
  });

  it('allows gateway-only access to public catalog reads but not protected routes', async () => {
    app = Fastify();
    app.addHook('onRequest', createRegistryAuthMiddleware('registry-internal-secret'));
    const catalog = vi.fn(async (request: { authContext?: unknown }) => ({ auth: request.authContext ?? null }));
    const protectedHandler = vi.fn(async () => ({ ok: true }));
    app.get('/api/v1/packages', catalog);
    app.get('/api/v1/my/packages', protectedHandler);
    await app.ready();

    const headers = { 'x-internal-secret': 'registry-internal-secret' };
    const publicRead = await app.inject({ method: 'GET', url: '/api/v1/packages', headers });
    expect(publicRead.statusCode).toBe(200);
    expect(publicRead.json()).toEqual({ auth: null });

    const protectedRead = await app.inject({ method: 'GET', url: '/api/v1/my/packages', headers });
    expect(protectedRead.statusCode).toBe(401);
    expect(protectedRead.json()).toMatchObject({ error: { code: 'KB_AUTH_REQUIRED' } });
    expect(protectedHandler).not.toHaveBeenCalled();
  });
});
