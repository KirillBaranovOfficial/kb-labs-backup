/**
 * @module @kb-labs/gateway-app/internal/routes
 * Service-to-service endpoints — NOT for external clients.
 * Auth: x-internal-secret header (GATEWAY_INTERNAL_SECRET env var).
 *
 * POST /internal/dispatch      — invoke adapter method on a connected host
 * POST /internal/resolve-host  — resolve hostId for namespace + strategy
 */

import type { FastifyInstance } from 'fastify';
import { globalDispatcher } from '../hosts/dispatcher.js';
import type { HostRegistry } from '../hosts/registry.js';
import { getClientByHandle } from '@kb-labs/gateway-auth';

export function registerInternalRoutes(
  scope: FastifyInstance,
  internalSecret: string | undefined,
  hostRegistry: HostRegistry,
  cache?: import('@kb-labs/core-platform').ICache,
): void {
  scope.post('/internal/dispatch', { config: { uxFixture: { validResult: 'error-envelope' } },
    schema: {
      body: {
        type: 'object',
        properties: {
          namespaceId: { type: 'string' },
          hostId: { type: 'string' },
          adapter: { type: 'string' },
          method: { type: 'string' },
          args: { type: 'array' },
        },
        required: ['namespaceId', 'adapter', 'method'],
      },
      querystring: { type: 'object', properties: {}, additionalProperties: false },
      response: { 200: { type: 'object', properties: { result: {} }, required: ['result'] }, 400: { type: 'object', properties: { error: { type: 'string' } }, required: ['error'] }, 403: { type: 'object', properties: { error: { type: 'string' } }, required: ['error'] }, 502: { type: 'object', properties: { error: { type: 'string' } }, required: ['error'] }, 503: { type: 'object', properties: { error: { type: 'string' }, namespaceId: { type: 'string' } }, required: ['error'] } },
    },
  }, async (request, reply) => {
    const provided = request.headers['x-internal-secret'];
    if (!internalSecret || provided !== internalSecret) {
      return reply.code(403).send({ error: 'Forbidden' });
    }

    const body = request.body as {
      namespaceId?: string;
      hostId?: string;
      adapter?: string;
      method?: string;
      args?: unknown[];
    };

    if (!body.namespaceId || !body.adapter || !body.method) {
      return reply.code(400).send({ error: 'Missing required fields: namespaceId, adapter, method' });
    }

    const hostId = body.hostId
      ?? globalDispatcher.firstHostWithCapability(body.namespaceId, body.adapter)
      ?? globalDispatcher.firstHost(body.namespaceId);
    if (!hostId) {
      return reply.code(503).send({
        error: 'No host connected',
        namespaceId: body.namespaceId,
      });
    }

    try {
      const result = await globalDispatcher.call(
        body.namespaceId,
        hostId,
        body.adapter,
        body.method,
        body.args ?? [],
      );
      return { result };
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      if (message.includes('Host not connected')) {
        return reply.code(503).send({ error: message });
      }
      return reply.code(502).send({ error: message });
    }
  });

  scope.get('/internal/auth/handle/:handle', { config: { uxFixture: { validResult: 'error-envelope' } },
    schema: { params: { type: 'object', properties: { handle: { type: 'string' } }, required: ['handle'] }, querystring: { type: 'object', properties: {}, additionalProperties: false }, response: { 200: { type: 'object', properties: { namespaceId: { type: 'string' }, name: { type: 'string' }, handle: { type: 'string' } }, required: ['namespaceId', 'name', 'handle'] }, 403: { type: 'object', properties: { error: { type: 'string' } }, required: ['error'] }, 404: { type: 'object', properties: { error: { type: 'string' } }, required: ['error'] }, 503: { type: 'object', properties: { error: { type: 'string' } }, required: ['error'] } } },
  }, async (request, reply) => {
    const provided = request.headers['x-internal-secret'];
    if (!internalSecret || provided !== internalSecret) {
      return reply.code(403).send({ error: 'Forbidden' });
    }
    if (!cache) {
      return reply.code(503).send({ error: 'Cache not available' });
    }
    const { handle } = request.params as { handle: string };
    const record = await getClientByHandle(cache, handle);
    if (!record) {
      return reply.code(404).send({ error: 'Handle not found' });
    }
    return { namespaceId: record.namespaceId, name: record.name, handle: record.handle };
  });

  scope.post('/internal/resolve-host', { config: { uxFixture: { validResult: 'error-envelope' } },
    schema: {
      body: {
        type: 'object',
        properties: {
          namespaceId: { type: 'string' },
          target: {
            type: 'object',
            properties: {
              hostId: { type: 'string' },
              hostSelection: { type: 'string' },
              repoFingerprint: { type: 'string' },
            },
          },
        },
      },
      querystring: { type: 'object', properties: {}, additionalProperties: false },
      response: { 200: { type: 'object', properties: { hostId: { type: 'string' }, strategy: { type: 'string' }, namespaceId: { type: 'string' } }, required: ['hostId', 'strategy', 'namespaceId'] }, 403: { type: 'object', properties: { error: { type: 'string' } }, required: ['error'] }, 404: { type: 'object', properties: { error: { type: 'string' } }, required: ['error'] } },
    },
  }, async (request, reply) => {
    const provided = request.headers['x-internal-secret'];
    if (!internalSecret || provided !== internalSecret) {
      return reply.code(403).send({ error: 'Forbidden' });
    }

    const body = request.body as {
      namespaceId?: string;
      target?: {
        hostId?: string;
        hostSelection?: string;
        repoFingerprint?: string;
      };
    };

    const namespaceId = body.namespaceId ?? 'default';
    const target = body.target ?? {};
    const strategy = (target.hostSelection ?? 'any-matching') as string;

    let hostId: string | undefined;

    if (strategy === 'pinned' && target.hostId) {
      const host = await hostRegistry.get(target.hostId, namespaceId);
      if (host?.status === 'online' || host?.status === 'reconnecting') {
        hostId = target.hostId;
      }
    } else {
      hostId = globalDispatcher.firstHostWithCapability(namespaceId, 'execution');
    }

    if (!hostId) {
      return reply.code(404).send({ error: 'No matching host found' });
    }

    return { hostId, strategy, namespaceId };
  });
}
