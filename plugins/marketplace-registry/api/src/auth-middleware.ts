import type { FastifyRequest, FastifyReply } from 'fastify';
import { readForwardedIdentity, type AuthContext } from '@kb-labs/gateway-contracts';
import { createErrorEnvelope } from '@kb-labs/core-platform';

declare module 'fastify' {
  interface FastifyRequest {
    authContext?: AuthContext;
  }
}

function isPublicCatalogRead(request: FastifyRequest): boolean {
  if (request.method !== 'GET') { return false; }
  const path = new URL(request.url, 'http://registry.local').pathname.replace(/\/+$/, '') || '/';
  return path === '/api/v1/packages' ||
    /^\/api\/v1\/packages\/[^/]+\/[^/]+(?:\/stats)?$/.test(path);
}

export function createRegistryAuthMiddleware(internalSecret?: string) {
  return async function registryGatewayIdentity(
    request: FastifyRequest,
    reply: FastifyReply,
  ): Promise<void> {
    const forwarded = readForwardedIdentity(request.headers, internalSecret);
    if (forwarded.kind === 'rejected' ||
      (forwarded.kind === 'service' && !isPublicCatalogRead(request))) {
      return reply.code(401).send({ error: createErrorEnvelope('KB_AUTH_REQUIRED') });
    }

    if (forwarded.kind === 'identity') { request.authContext = forwarded.auth; }
  };
}
