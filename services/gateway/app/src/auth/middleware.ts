import type { FastifyRequest, FastifyReply } from 'fastify';
import type { ICache } from '@kb-labs/core-platform';
import { createErrorEnvelope } from '@kb-labs/core-platform';
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
  ['/health/auth', new Set(['GET', 'HEAD'])], ['/ready', new Set(['GET', 'HEAD'])],
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
  const oauthSegments = routePath.split('/').filter(Boolean);
  if (method === 'GET' && oauthSegments[0] === 'auth' && oauthSegments[1] === 'oauth') {
    return oauthSegments.length === 4 && ['start', 'callback'].includes(oauthSegments[3] ?? '');
  }
  if (['GET', 'HEAD'].includes(method)) {
    if (/^\/(?:assets|static|studio\/assets)\/[^/]+(?:\/[^/]+)*\.[a-z0-9]{1,8}$/i.test(routePath)) { return true; }
  }
  return routePath.startsWith('/plugins/') && /\/widgets\/[^/]+\.[a-z0-9]{1,8}$/i.test(routePath);
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

declare module 'fastify' {
  interface FastifyRequest { authContext?: AuthContext; }
}

/** The single gateway authentication hook. It authenticates users first, then machine tokens. */
export function createAuthMiddleware(
  cache: ICache,
  jwtConfig: JwtConfig,
  options: { authEnabled?: boolean; internalSecret?: string; userAuth?: UserAuthMiddlewareDeps; webhookManifests?: WebhookManifest[] } = {},
) {
  const authEnabled = options.authEnabled !== false;
  const userAuth = options.userAuth ? createUserAuthMiddleware(options.userAuth) : undefined;
  return async function authMiddleware(request: FastifyRequest, reply: FastifyReply): Promise<void> {
    if (!authEnabled) { request.authContext = LOCAL_ADMIN_CONTEXT; return; }

    const rawPath = new URL(request.url, 'http://localhost').pathname;
    const routePath = rawPath.replace(/\/+/g, '/').replace(/\/+$/, '') || '/';
    if (isPlatformPublicRoute(request.method, routePath)) { return; }
    if (routePath.startsWith('/internal/')) {
      if (options.internalSecret && request.headers['x-internal-secret'] === options.internalSecret) { return; }
      return reply.code(401).send({ error: createErrorEnvelope('KB_AUTH_REQUIRED') });
    }
    // These routes are admitted only from webhook manifest declarations. The
    // registered platform handler verifies the declaration's auth before dispatch.
    if (isDeclaredWebhookRoute(request.method, routePath, options.webhookManifests ?? [])) { return; }

    if (userAuth) {
      await userAuth(request, reply);
      if (reply.sent) { return; }
      if (request.userAuthContext?.source === 'cookie' || request.userAuthContext?.source === 'bearer' || request.userAuthContext?.source === 'api-token') { return; }
    }

    const queryToken = (request.query as Record<string, string | undefined>)['access_token'];
    const token = extractBearerToken(request.headers.authorization) ?? queryToken ?? null;
    if (!token && request.userAuthContext) { return; }
    if (request.userAuthContext?.source === 'bearer' || request.userAuthContext?.source === 'api-token') { return; }
    if (!token) { return reply.code(401).send({ error: createErrorEnvelope('KB_AUTH_REQUIRED') }); }
    const authContext = await resolveToken(token, cache, jwtConfig).catch(() => null);
    if (!authContext) { return reply.code(401).send({ error: createErrorEnvelope('KB_AUTH_REQUIRED') }); }
    request.authContext = authContext;
  };
}
