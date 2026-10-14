/**
 * Auth routes — machine-client endpoints for agent registration and token management.
 *
 * POST /auth/register  — register new agent, get clientId + clientSecret
 *                        (gated behind MACHINE_REGISTER permission; see gate below)
 * POST /auth/token     — exchange credentials for JWT pair
 * POST /auth/refresh   — rotate refresh token (machine body path); user cookie
 *                        path is handled first if userRefreshFn is provided
 *
 * NOTE: GET /auth/me is NOT registered here — it lives in registerUserAuthRoutes
 * because it must handle both user-cookie and machine-Bearer contexts in one handler.
 */

import type { FastifyInstance, FastifyRequest, FastifyReply } from 'fastify';
import type { AuthService, IPolicyDecisionPoint } from '@kb-labs/gateway-auth';
import {
  RegisterRequestSchema,
  TokenRequestSchema,
  RefreshRequestSchema,
} from '@kb-labs/gateway-contracts';
import { PERMISSIONS } from '@kb-labs/core-contracts';
import { sendAuthError } from './error-response.js';
import { COOKIE_REFRESH } from './user-cookies.js';
import { authRejectionEnvelope, gatewayRequestUrl } from './tokens.js';
import { optionalRequestBodySchema, uxErrorResponses } from '../route-schema.js';

const string = { type: 'string' } as const;
const tokenResponse = {
  type: 'object',
  properties: { accessToken: string, refreshToken: string, expiresIn: { type: 'number' }, tokenType: { type: 'string', const: 'Bearer' } },
  required: ['accessToken', 'refreshToken', 'expiresIn', 'tokenType'],
} as const;

export interface MachineAuthRoutesUserExt {
  /** Called when a user refresh cookie is detected in POST /auth/refresh.
   *  Must write the reply and return true; return false to fall through to
   *  the machine body-refresh path. */
  userRefreshFn: (request: FastifyRequest, reply: FastifyReply) => Promise<boolean>;
  userLogoutFn: (refreshToken: string) => Promise<void>;
  /** PDP for checking MACHINE_REGISTER permission on cookie-authenticated admin users. */
  pdp: IPolicyDecisionPoint;
}

async function requireMachineRegistrationIdentity(request: FastifyRequest, reply: FastifyReply): Promise<void> {
  if (request.authContext || request.userAuthContext) { return; }
  sendAuthError(reply, 401, undefined, undefined, undefined, { gatewayUrl: gatewayRequestUrl(request) });
}

export function registerAuthRoutes(
  app: FastifyInstance,
  authService: AuthService,
  userExt?: MachineAuthRoutesUserExt,
): void {
  // Register new agent.
  // Requires MACHINE_REGISTER permission. Auth checks:
  //   - Anonymous (no cookie, no Bearer) → 401 from this route's declared auth hook
  //   - Cookie-auth user without permission → 403 (PDP check below)
  //   - Cookie-auth admin with MACHINE_REGISTER → 200
  //   - Bearer-auth machine with machine:register in JWT permissions → 200
  app.post('/auth/register', { config: { auth: 'route' }, onRequest: requireMachineRegistrationIdentity, schema: { tags: ['Auth'], summary: 'Register new agent and get credentials', querystring: { type: 'object', properties: {}, additionalProperties: false }, body: { type: 'object', properties: { name: { type: 'string', minLength: 1, maxLength: 64 }, capabilities: { type: 'array', items: string }, publicKey: string, handle: { type: 'string', pattern: '^[a-z0-9-]{2,32}$' }, email: { type: 'string', format: 'email' } }, required: ['name'], additionalProperties: false }, response: { 200: { type: 'object', properties: { clientId: string, clientSecret: string, hostId: string, namespaceId: string, handle: string }, required: ['clientId', 'clientSecret', 'hostId', 'namespaceId'] }, ...uxErrorResponses([400, 401, 403, 409]) } } }, async (request, reply) => {
    // Gate behind MACHINE_REGISTER permission.
    // Two auth paths: cookie-authenticated user (userAuthContext) or Bearer machine (authContext).
    const userCtx = request.userAuthContext;
    const machineCtx = request.authContext;

    if (userCtx && userExt?.pdp) {
      // Cookie-authenticated user — check via PDP.
      const decision = await userExt.pdp.check(
        { userId: userCtx.userId, tenantId: userCtx.tenantId, type: 'user' },
        PERMISSIONS.MACHINE_REGISTER,
      );
      if (!decision.allow) {
        return sendAuthError(reply, 403, PERMISSIONS.MACHINE_REGISTER);
      }
    } else if (machineCtx) {
      // Bearer-authenticated machine — check permissions embedded in JWT.
      if (!machineCtx.permissions.includes(PERMISSIONS.MACHINE_REGISTER)) {
        return sendAuthError(reply, 403, PERMISSIONS.MACHINE_REGISTER);
      }
    } else {
      // No auth context at all (should have been caught by middleware, but guard defensively).
      return sendAuthError(reply, 401, undefined, 'Authentication required');
    }

    const parsed = RegisterRequestSchema.safeParse(request.body);
    if (!parsed.success) {
      return reply.code(400).send({ error: 'Bad Request', issues: parsed.error.issues });
    }

    const { name, capabilities, publicKey, handle, email } = parsed.data;
    try {
      const result = await authService.register({ name, capabilities, publicKey, handle, email });
      return reply.code(200).send(result);
    } catch (err) {
      if (err instanceof Error && (err as NodeJS.ErrnoException).code === 'HANDLE_TAKEN') {
        return reply.code(409).send({ error: 'Conflict', message: err.message });
      }
      throw err;
    }
  });

  // Issue token pair (machine credentials → JWT pair).
  app.post('/auth/token', { config: { uxFixture: { validResult: 'error-envelope' } }, schema: { tags: ['Auth'], summary: 'Issue JWT token pair', querystring: { type: 'object', properties: {}, additionalProperties: false }, body: { type: 'object', properties: { clientId: string, clientSecret: string }, required: ['clientId', 'clientSecret'], additionalProperties: false }, response: { 200: tokenResponse, ...uxErrorResponses([400, 401]) } } }, async (request, reply) => {
    const parsed = TokenRequestSchema.safeParse(request.body);
    if (!parsed.success) {
      return reply.code(400).send({ error: 'Bad Request', issues: parsed.error.issues });
    }

    const tokens = await authService.issueTokens(parsed.data.clientId, parsed.data.clientSecret);
    if (!tokens) {
      return sendAuthError(reply, 401, undefined, 'Invalid credentials', 'KB_AUTH_INVALID_CREDENTIALS');
    }

    return reply.send(tokens);
  });

  // Refresh token pair.
  // If a user refresh cookie is detected AND userExt.userRefreshFn is provided,
  // the user path is handled first. Otherwise, falls back to machine body refresh.
  app.post('/auth/refresh', { config: { uxFixture: { validResult: 'error-envelope' } }, schema: { tags: ['Auth'], summary: 'Refresh JWT token pair (machine or user)', querystring: { type: 'object', properties: {}, additionalProperties: false }, body: optionalRequestBodySchema({ type: 'object', properties: { refreshToken: string }, required: ['refreshToken'], additionalProperties: false }), response: { 200: { oneOf: [tokenResponse, { type: 'object', properties: { ok: { type: 'boolean', const: true } }, required: ['ok'], additionalProperties: false }] }, ...uxErrorResponses([400, 401]) } } }, async (request, reply) => {
    // User cookie refresh — handled by the user-auth subsystem if wired.
    const cookies = (request as FastifyRequest & { cookies?: Record<string, string | undefined> }).cookies;
    const refreshCookie = cookies?.[COOKIE_REFRESH];
    if (refreshCookie && userExt) {
      const handled = await userExt.userRefreshFn(request, reply);
      if (handled) {return;}
    }

    // Machine body refresh (existing logic).
    const parsed = RefreshRequestSchema.safeParse(request.body);
    if (!parsed.success) {
      return reply.code(400).send({ error: 'Bad Request', issues: parsed.error.issues });
    }

    const verification = await authService.refreshTokens(parsed.data.refreshToken);
    if (!verification.ok) {
      request.log.warn({ event: 'gateway.auth.rejected', requestId: String(request.id), reason: verification.reason }, 'Gateway authentication rejected');
      return reply.code(401).send({ ok: false, error: authRejectionEnvelope(verification.reason, gatewayRequestUrl(request), String(request.id)) });
    }

    return reply.send(verification.payload);
  });

  app.post<{ Body: { refreshToken?: string } }>('/auth/logout/cli', { config: { uxFixture: { validResult: 'error-envelope' } }, schema: { tags: ['Auth'], summary: 'Revoke a CLI session or machine refresh token', querystring: { type: 'object', properties: {}, additionalProperties: false }, body: { type: 'object', properties: { refreshToken: string }, additionalProperties: false }, response: { 200: { type: 'object', properties: { ok: { type: 'boolean' } }, required: ['ok'] }, ...uxErrorResponses([400]) } } }, async (request, reply) => {
    const refreshToken = request.body?.refreshToken;
    if (typeof refreshToken !== 'string' || !refreshToken) {
      return reply.code(400).send({ error: 'Bad Request', message: 'refreshToken is required' });
    }
    const tokenKind = await authService.classifyRefreshToken(refreshToken);
    if (tokenKind.ok && tokenKind.payload.kind === 'machine') {
      await authService.revokeRefreshToken(refreshToken);
    } else if (tokenKind.ok && tokenKind.payload.kind === 'user') {
      await userExt?.userLogoutFn(refreshToken);
    } else if (!tokenKind.ok) {
      request.log.warn({ event: 'gateway.auth.rejected', requestId: String(request.id), reason: tokenKind.reason }, 'Gateway authentication rejected');
    }
    return reply.send({ ok: true });
  });
}
