import type { FastifyRequest, FastifyReply } from 'fastify';
import { AuthContextSchema, type AuthContext } from '@kb-labs/gateway-contracts';
import { createErrorEnvelope } from '@kb-labs/core-platform';

declare module 'fastify' {
  interface FastifyRequest {
    authContext?: AuthContext;
  }
}

function header(request: FastifyRequest, name: string): string | undefined {
  const value = request.headers[name];
  return typeof value === 'string' ? value : undefined;
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
    const suppliedSecret = header(request, 'x-internal-secret');
    const permissionsJson = header(request, 'x-kb-auth-permissions');
    let permissions: string[] | undefined;
    try {
      const parsed: unknown = JSON.parse(permissionsJson ?? '[]');
      if (!Array.isArray(parsed) || !parsed.every((permission) => typeof permission === 'string')) {
        throw new Error('Invalid forwarded permissions');
      }
      permissions = parsed;
    } catch {
      permissions = undefined;
    }

    const type = header(request, 'x-kb-auth-type');
    const userId = header(request, 'x-kb-auth-user-id');
    const namespaceId = header(request, 'x-kb-auth-namespace-id');
    const tier = header(request, 'x-kb-auth-tier');
    const hasIdentity = [type, userId, namespaceId, tier, permissionsJson].some((value) => value !== undefined);
    const identity = hasIdentity
      ? AuthContextSchema.safeParse({ type, userId, namespaceId, tier, permissions })
      : undefined;
    if (!internalSecret || suppliedSecret !== internalSecret ||
      (hasIdentity && !identity?.success) || (!hasIdentity && !isPublicCatalogRead(request))) {
      return reply.code(401).send({ error: createErrorEnvelope('KB_AUTH_REQUIRED') });
    }

    if (identity?.success) { request.authContext = identity.data; }
  };
}
