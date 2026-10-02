import type { FastifyRequest, FastifyReply } from 'fastify';
import type { ICache } from '@kb-labs/core-platform';
import { sendAuthError } from './error-response.js';
import type { AuthContext } from '@kb-labs/gateway-contracts';
import type { JwtConfig } from '@kb-labs/gateway-auth';
import type { ManifestV3 } from '@kb-labs/plugin-contracts';
import { PERMISSIONS } from '@kb-labs/core-contracts';
import { resolveToken, extractBearerToken } from './tokens.js';
import { createUserAuthMiddleware, type UserAuthMiddlewareDeps } from './user-auth-middleware.js';

export const LOCAL_ADMIN_CONTEXT: AuthContext = {
  type: 'machine', userId: 'local-admin', namespaceId: 'local', tier: 'enterprise',
  permissions: Object.values(PERMISSIONS),
};

const PUBLIC_ROUTES = new Map<string, ReadonlySet<string>>([
  ['/health', new Set(['GET', 'HEAD'])], ['/health/adapters', new Set(['GET', 'HEAD'])],
  // The route has its own loopback-peer, loopback-Host, and no-forwarding-header guard.
  ['/health/auth', new Set(['GET', 'HEAD'])],
  ['/ready', new Set(['GET', 'HEAD'])],
  ['/', new Set(['GET', 'HEAD'])], ['/login', new Set(['GET', 'HEAD'])],
  ['/favicon.ico', new Set(['GET', 'HEAD'])], ['/mf-manifest.json', new Set(['GET', 'HEAD'])],
  ['/studio', new Set(['GET', 'HEAD'])], ['/hosts/register', new Set(['POST'])],
  ['/auth/token', new Set(['POST'])], ['/auth/refresh', new Set(['POST'])],
  ['/auth/login', new Set(['POST'])], ['/auth/login/cli', new Set(['POST'])],
  ['/auth/refresh/cli', new Set(['POST'])], ['/auth/logout/cli', new Set(['POST'])],
  ['/auth/activate', new Set(['POST'])], ['/auth/password/reset', new Set(['POST'])],
  ['/auth/providers', new Set(['GET'])],
]);

type WebhookManifest = { pluginId: string; manifest: ManifestV3 };

export function isPlatformPublicRoute(method: string, routePath: string): boolean {
  if (PUBLIC_ROUTES.get(routePath)?.has(method)) { return true; }
  if (method === 'GET' && isRegistryPublicReadRoute(routePath)) { return true; }
  const oauthSegments = routePath.split('/').filter(Boolean);
  if (method === 'GET' && oauthSegments[0] === 'auth' && oauthSegments[1] === 'oauth') {
    return oauthSegments.length === 4 && ['start', 'callback'].includes(oauthSegments[3] ?? '');
  }
  if (['GET', 'HEAD'].includes(method)) {
    if (/^\/(?:assets|static|studio\/assets)\/[^/]+(?:\/[^/]+)*\.[a-z0-9]{1,8}$/i.test(routePath)) { return true; }
  }
  return routePath.startsWith('/plugins/') && /\/widgets\/[^/]+\.[a-z0-9]{1,8}$/i.test(routePath);
}

function isRegistryPublicReadRoute(routePath: string): boolean {
  const path = routePath.replace(/\/+$/, '') || '/';
  const reads = '(?:api/v1/packages|api/v1/packages/[^/]+/[^/]+(?:/stats)?)';
  return new RegExp(`^/api/v1/marketplace-registry/${reads}$`).test(path) ||
    new RegExp(`^/api/v1/projects/[^/]+/marketplace-registry/${reads}$`).test(path);
}

function isStudioFallbackRoute(routePath: string): boolean {
  const segments = routePath.split('/').filter(Boolean);
  const reserved = new Set(['api', 'auth', 'health', 'ready', 'metrics', 'observability', 'hosts', 'clients', 'internal', 'webhooks', 'telemetry', 'docs', 'docs-all', 'openapi.json', 'openapi-merged.json', 'echo']);
  return segments.length > 0 && !reserved.has(segments[0] ?? '') && !routePath.slice(1).includes('.');
}

export function isDeclaredWebhookRoute(method: string, routePath: string, manifests: WebhookManifest[]): boolean {
  if (method !== 'POST') { return false; }
  const parts = routePath.split('/').filter(Boolean);
  if (parts[0] !== 'webhooks' || (parts.length !== 3 && parts.length !== 4)) { return false; }
  let pluginId: string;
  try { pluginId = decodeURIComponent(parts[1] ?? ''); } catch { return false; }
  const event = parts[2];
  const multi = parts.length === 4;
  return manifests.some(({ pluginId: declaredId, manifest }) => declaredId === pluginId &&
    manifest.webhooks?.handlers.some((handler) => handler.event === event && Boolean(handler.multi) === multi && Boolean(handler.auth)));
}

function isCorsPreflight(request: FastifyRequest): boolean {
  return request.method === 'OPTIONS' && Boolean(request.headers.origin) &&
    typeof request.headers['access-control-request-method'] === 'string';
}

function sendCorsPreflight(request: FastifyRequest, reply: FastifyReply): FastifyReply {
  const requestedMethod = request.headers['access-control-request-method'];
  const allowedMethods = new Set(['GET', 'POST', 'PUT', 'PATCH', 'DELETE']);
  const method = typeof requestedMethod === 'string' && allowedMethods.has(requestedMethod.toUpperCase())
    ? requestedMethod.toUpperCase()
    : '';
  const requestedHeaders = request.headers['access-control-request-headers'];
  const allowedHeaders = new Set(['authorization', 'content-type', 'mcp-session-id']);
  const headers = typeof requestedHeaders === 'string'
    ? requestedHeaders.split(',').map((header) => header.trim().toLowerCase()).filter((header) => allowedHeaders.has(header))
    : [];
  reply.header('Access-Control-Allow-Origin', '*').header('Vary', 'Origin');
  if (method) { reply.header('Access-Control-Allow-Methods', method); }
  if (headers.length > 0) { reply.header('Access-Control-Allow-Headers', headers.join(', ')); }
  return reply.code(204).send();
}

const FORWARDED_IDENTITY_HEADERS = [
  'x-kb-auth-type', 'x-kb-auth-user-id', 'x-kb-auth-namespace-id', 'x-kb-auth-tier', 'x-kb-auth-permissions',
] as const;

function removeClientIdentityHeaders(request: FastifyRequest): void {
  delete request.headers['x-internal-secret'];
  for (const name of FORWARDED_IDENTITY_HEADERS) { delete request.headers[name]; }
}

function attachGatewayIdentity(request: FastifyRequest, internalSecret?: string): void {
  const auth = request.authContext;
  if (!internalSecret) { return; }
  request.headers['x-internal-secret'] = internalSecret;
  if (!auth) { return; }
  request.headers['x-kb-auth-type'] = auth.type;
  request.headers['x-kb-auth-user-id'] = auth.userId;
  request.headers['x-kb-auth-namespace-id'] = auth.namespaceId;
  request.headers['x-kb-auth-tier'] = auth.tier;
  request.headers['x-kb-auth-permissions'] = JSON.stringify(auth.permissions);
}

declare module 'fastify' {
  interface FastifyRequest { authContext?: AuthContext; }
}

/** The single gateway authentication hook. It authenticates users first, then machine tokens. */
export function createAuthMiddleware(
  cache: ICache,
  jwtConfig: JwtConfig,
  options: { authEnabled?: boolean; internalSecret?: string; userAuth?: UserAuthMiddlewareDeps; webhookManifests?: WebhookManifest[]; studioEnabled?: boolean } = {},
) {
  const authEnabled = options.authEnabled !== false;
  const userAuth = options.userAuth ? createUserAuthMiddleware(options.userAuth) : undefined;
  return async function authMiddleware(request: FastifyRequest, reply: FastifyReply): Promise<void> {
    if (isCorsPreflight(request)) { sendCorsPreflight(request, reply); return; }
    if (!request.url.startsWith('/internal/')) { removeClientIdentityHeaders(request); }
    if (!authEnabled) {
      request.authContext = LOCAL_ADMIN_CONTEXT;
      attachGatewayIdentity(request, options.internalSecret);
      return;
    }

    const rawPath = new URL(request.url, 'http://localhost').pathname;
    const routePath = rawPath.replace(/\/+/g, '/').replace(/\/+$/, '') || '/';
    const registryPublicRead = request.method === 'GET' && isRegistryPublicReadRoute(routePath);
    if (isPlatformPublicRoute(request.method, routePath) && !registryPublicRead) { return; }
    if (!registryPublicRead && options.studioEnabled && ['GET', 'HEAD'].includes(request.method) && isStudioFallbackRoute(routePath)) { return; }
    const optionalRegistryToken = (request.query as Record<string, string | undefined>)['access_token'];
    if (registryPublicRead && !extractBearerToken(request.headers.authorization) && !optionalRegistryToken && !request.headers.cookie) {
      attachGatewayIdentity(request, options.internalSecret);
      return;
    }
    if (routePath.startsWith('/internal/')) {
      if (options.internalSecret && request.headers['x-internal-secret'] === options.internalSecret) { return; }
      return sendAuthError(reply, 401);
    }
    // These routes are admitted only from webhook manifest declarations. The
    // registered platform handler verifies the declaration's auth before dispatch.
    if (isDeclaredWebhookRoute(request.method, routePath, options.webhookManifests ?? [])) { return; }

    if (userAuth) {
      await userAuth(request, reply);
      if (reply.sent) { return; }
      if (request.userAuthContext?.source === 'cookie' || request.userAuthContext?.source === 'bearer' || request.userAuthContext?.source === 'api-token') {
        attachGatewayIdentity(request, options.internalSecret);
        return;
      }
    }

    const queryToken = (request.query as Record<string, string | undefined>)['access_token'];
    const token = extractBearerToken(request.headers.authorization) ?? queryToken ?? null;
    if (!token && request.userAuthContext) { return; }
    if (request.userAuthContext?.source === 'bearer' || request.userAuthContext?.source === 'api-token') { return; }
    if (!token) {
      if (registryPublicRead) { return; }
      return sendAuthError(reply, 401);
    }
    const authContext = await resolveToken(token, cache, jwtConfig).catch(() => null);
    if (!authContext) { return sendAuthError(reply, 401); }
    request.authContext = authContext;
    attachGatewayIdentity(request, options.internalSecret);
  };
}
