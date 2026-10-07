/**
 * Integration tests for user-auth HTTP routes (ADR-0020, Phase 1.16).
 *
 * Spins up a real Fastify instance with in-memory stores, both middleware
 * layers, and all user-auth routes. Uses fastify.inject() — no network.
 *
 * Coverage:
 *   POST  /auth/login                  — happy, bad creds (CD-8 shape)
 *   POST  /auth/login/cli              — happy (no cookies!), bad creds, missing fields
 *   POST  /auth/refresh/cli            — happy, missing/invalid refreshToken
 *   POST  /auth/register (Bearer user token) — admin 200, member 403
 *   POST  /auth/logout                 — happy, CSRF guard
 *   POST  /auth/refresh (user cookie)  — happy, missing cookie, tampered
 *   GET   /auth/me                     — user context + no role field
 *   GET   /auth/providers              — public
 *   GET   /auth/permissions            — cookie auth
 *   POST  /auth/activate               — happy, invalid token
 *   POST  /auth/password/change        — happy, wrong current, CSRF guard
 *   GET   /auth/sessions               — list
 *   POST  /auth/sessions/:fam/revoke   — revoke
 *   POST  /auth/sessions/revoke-all    — revoke all except current
 *   POST  /auth/invites                — admin only, 403 for member
 *   GET   /auth/invites                — admin only
 *   GET   /auth/users                  — admin only
 *   POST  /auth/users/:id/disable      — admin only, CD-1 immediate effect
 *   POST  /auth/users/:id/enable       — admin only
 *   POST  /auth/invites/:id/revoke     — admin only
 */

import { describe, it, expect, beforeEach } from 'vitest';
import Fastify, { type FastifyInstance } from 'fastify';
import fastifyCookie from '@fastify/cookie';
import { createInMemoryDocumentDatabase, createInMemoryKVStore } from '@kb-labs/sdk/testing';
import type { ICache } from '@kb-labs/core-platform';
import { PERMISSIONS } from '@kb-labs/core-contracts';
import {
  UsersStore,
  CredentialsStore,
  MembershipsStore,
  SessionsStore,
  ApiTokensStore,
  InvitesStore,
  PasswordResetStore,
  AuthAuditStore,
  ProviderRegistry,
  type SessionsStoreOptions,
  createTenantResolver,
  createUserAuthService,
  createEmailPasswordProvider,
  createPasswordPolicy,
  createStubPDP,
  createRateLimiter,
  AuthService,
  type JwtConfig,
} from '@kb-labs/gateway-auth';
import { createUserAuthMiddleware } from '../user-auth-middleware.js';
import { createAuthMiddleware } from '../middleware.js';
import { registerAuthRoutes, type MachineAuthRoutesUserExt } from '../routes.js';
import { registerUserAuthRoutes, createUserRefreshFn } from '../user-routes.js';
import { COOKIE_ACCESS, COOKIE_REFRESH, COOKIE_CSRF } from '../user-cookies.js';
import { registerGatewayUxErrorResponses } from '../../ux-boundary.js';
import bcryptjs from 'bcryptjs';

// Minimal no-op ICache stub used to satisfy machine auth service constructor.
// Machine auth endpoints are not exercised in this test suite.
const fakeCache: ICache = {
  get: async () => null,
  set: async () => {},
  delete: async () => {},
  clear: async () => {},
} as unknown as ICache;

// ── Constants ─────────────────────────────────────────────────────────────────

const HOUR = 60 * 60 * 1000;
const SESSION_OPTS: SessionsStoreOptions = { refreshTtlMs: HOUR, graceWindowMs: 5_000 };
const JWT: JwtConfig = { secret: 'test-secret-at-least-32-chars-long!!' };
const TENANT_ID = 'kb-cloud';
const HOST = `${TENANT_ID}.kblabs.ru`;

// ── App factory ───────────────────────────────────────────────────────────────

interface TestCtx {
  app: FastifyInstance;
  users: UsersStore;
  credentials: CredentialsStore;
  memberships: MembershipsStore;
  sessions: SessionsStore;
  apiTokens: ApiTokensStore;
  invites: InvitesStore;
  pdp: ReturnType<typeof createStubPDP>;
}

interface BuildAppOpts {
  /** When set, wires a fixed-window rate limiter into the login route. */
  rateLimit?: { loginPerIpPerMinute: number; loginPerEmailPerMinute: number };
  /** When true, disables auth middleware (LOCAL_ADMIN context injected). */
  authDisabled?: boolean;
  /** Tenant used as the final fallback when Host resolves no tenant and the body
   *  carries none either. Defaults to TENANT_ID so existing tests are unaffected. */
  bootstrapTenantId?: string;
}

async function buildApp(opts: BuildAppOpts = {}): Promise<TestCtx> {
  const docs = createInMemoryDocumentDatabase();

  const users = new UsersStore(docs);
  const credentials = new CredentialsStore(docs);
  const memberships = new MembershipsStore(docs);
  const sessions = new SessionsStore(docs, SESSION_OPTS);
  const apiTokens = new ApiTokensStore(docs);
  const invites = new InvitesStore(docs);
  const passwordResets = new PasswordResetStore(docs);
  const authAudit = new AuthAuditStore(docs);
  const providers = new ProviderRegistry();

  const emailPwd = createEmailPasswordProvider({
    users,
    credentials,
    tenantId: TENANT_ID,
    bcryptCost: 4,
  });
  providers.register(emailPwd);

  const pdp = createStubPDP({ memberships });

  const passwordPolicy = createPasswordPolicy({
    minLength: 8,
    maxLength: 256,
    hibpEnabled: false,
  });

  const userAuthService = createUserAuthService({
    users,
    credentials,
    memberships,
    sessions,
    invites,
    passwordResets,
    providers,
    passwordPolicy,
    jwtConfig: JWT,
    accessTtlSec: 900,
    refreshTtlSec: HOUR / 1000,
    bcryptCost: 4,
  });

  const tenantResolver = createTenantResolver({ pattern: '{tenant}.kblabs.ru' });

  const rateLimiter = opts.rateLimit ? createRateLimiter(createInMemoryKVStore()) : undefined;

  const userRouteDeps = {
    userAuthService,
    users,
    memberships,
    sessions,
    apiTokens,
    invites,
    authAudit,
    providers,
    pdp,
    tenantResolver,
    bootstrapTenantId: opts.bootstrapTenantId ?? TENANT_ID,
    cookieOpts: { cookieSecure: false },
    accessTtlSec: 900,
    refreshTtlSec: HOUR / 1000,
    inviteTtlMs: 7 * 24 * HOUR,
    jwtConfig: JWT,
    ...(rateLimiter ? { rateLimiter, authRateLimit: opts.rateLimit } : {}),
  };

  // Machine auth service — used only to satisfy registerAuthRoutes constructor.
  // Machine endpoints (register, token) are not exercised in this test suite.
  const machineAuthService = new AuthService(fakeCache, JWT);

  const userExt: MachineAuthRoutesUserExt = {
    userRefreshFn: createUserRefreshFn({
      userAuthService,
      cookieOpts: { cookieSecure: false },
    }),
    userLogoutFn: (refreshToken) => userAuthService.logout(refreshToken),
    pdp,
  };

  const app = Fastify({ logger: false, trustProxy: true });
  registerGatewayUxErrorResponses(app);
  await app.register(fastifyCookie);

  // User-auth middleware runs first (before machine Bearer check).
  app.addHook('onRequest', createUserAuthMiddleware({ users, sessions, apiTokens, pdp, tenantResolver, jwtConfig: JWT }));

  // Machine auth middleware — skips if userAuthContext already set.
  app.addHook('onRequest', createAuthMiddleware(fakeCache, JWT, { authEnabled: !opts.authDisabled }));

  // Machine routes (/auth/register, /auth/token, /auth/refresh with user cookie ext).
  registerAuthRoutes(app, machineAuthService, userExt);

  // User-auth routes (all new user endpoints + /auth/me).
  registerUserAuthRoutes(app, userRouteDeps);

  await app.ready();
  return { app, users, credentials, memberships, sessions, apiTokens, invites, pdp };
}

// ── Seed helpers ──────────────────────────────────────────────────────────────

/**
 * Creates an admin user directly (not via bootstrap) with a bcrypt-hashed
 * password so the login flow works correctly.
 */
async function seedAdmin(
  ctx: Pick<TestCtx, 'users' | 'credentials' | 'memberships'>,
  opts: { userId?: string; tenantId?: string; email?: string; password?: string } = {},
): Promise<{ userId: string; tenantId: string; email: string; password: string }> {
  const userId = opts.userId ?? 'admin-1';
  const tenantId = opts.tenantId ?? TENANT_ID;
  const email = opts.email ?? 'admin@test.com';
  const password = opts.password ?? 'Password123!';

  await ctx.users.create({ userId, tenantId, email, status: 'active' });
  const hash = await bcryptjs.hash(password, 4);
  await ctx.credentials.setCredential({ userId, providerId: 'email-password', hash });
  await ctx.memberships.addMembership({ userId, tenantId, groupId: 'tenant-admin' });

  return { userId, tenantId, email, password };
}

/**
 * Performs a login and returns the cookies as a single header string.
 */
async function loginAndGetCookies(
  app: FastifyInstance,
  opts: { email: string; password: string; host?: string },
): Promise<{ cookieHeader: string; csrfToken: string }> {
  const r = await app.inject({
    method: 'POST',
    url: '/auth/login',
    headers: { host: opts.host ?? HOST },
    payload: { email: opts.email, password: opts.password },
  });

  if (r.statusCode !== 200) {
    throw new Error(`Login failed: ${r.statusCode} ${r.body}`);
  }

  const setCookies = r.headers['set-cookie'] as string[] | string | undefined;
  const cookieList = Array.isArray(setCookies) ? setCookies : [setCookies ?? ''];

  const parsed: Record<string, string> = {};
  for (const c of cookieList) {
    const [kv] = c.split(';');
    if (!kv) {continue;}
    const eq = kv.indexOf('=');
    if (eq > 0) {
      parsed[kv.slice(0, eq).trim()] = kv.slice(eq + 1).trim();
    }
  }

  const cookieHeader = Object.entries(parsed)
    .map(([k, v]) => `${k}=${v}`)
    .join('; ');

  return { cookieHeader, csrfToken: parsed[COOKIE_CSRF] ?? '' };
}

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('POST /auth/login', () => {
  let ctx: TestCtx;

  beforeEach(async () => {
    ctx = await buildApp();
    await seedAdmin(ctx);
  });

  it('returns 200 and sets three session cookies on success', async () => {
    const r = await ctx.app.inject({
      method: 'POST',
      url: '/auth/login',
      headers: { host: HOST },
      payload: { email: 'admin@test.com', password: 'Password123!' },
    });

    expect(r.statusCode).toBe(200);
    const cookies = r.headers['set-cookie'] as string[];
    const names = cookies.map((c) => c.split('=')[0]);
    expect(names).toContain(COOKIE_ACCESS);
    expect(names).toContain(COOKIE_REFRESH);
    expect(names).toContain(COOKIE_CSRF);
  });

  it('returns the shared credentials envelope on wrong password (CD-8)', async () => {
    const r = await ctx.app.inject({
      method: 'POST',
      url: '/auth/login',
      headers: { host: HOST },
      payload: { email: 'admin@test.com', password: 'WrongPassword99!' },
    });

    expect(r.statusCode).toBe(401);
    expect(r.json()).toMatchObject({ ok: false, error: { code: 'KB_AUTH_INVALID_CREDENTIALS' } });
  });

  it('returns 401 with identical shape on unknown email (CD-8)', async () => {
    const r = await ctx.app.inject({
      method: 'POST',
      url: '/auth/login',
      headers: { host: HOST },
      payload: { email: 'nobody@test.com', password: 'Password123!' },
    });

    expect(r.statusCode).toBe(401);
    expect(r.json()).toMatchObject({ ok: false, error: { code: 'KB_AUTH_INVALID_CREDENTIALS' } });
  });

  it('returns 400 when email is missing', async () => {
    const r = await ctx.app.inject({
      method: 'POST',
      url: '/auth/login',
      headers: { host: HOST },
      payload: { password: 'Password123!' },
    });
    expect(r.statusCode).toBe(400);
  });

  it('returns 400 when password is missing', async () => {
    const r = await ctx.app.inject({
      method: 'POST',
      url: '/auth/login',
      headers: { host: HOST },
      payload: { email: 'admin@test.com' },
    });
    expect(r.statusCode).toBe(400);
  });

  it('falls back to bootstrapTenantId when Host resolves no tenant and body carries none (direct/local gateway access, e.g. localhost:4000 — regression for the kb-create bootstrap-admin login bug)', async () => {
    const r = await ctx.app.inject({
      method: 'POST',
      url: '/auth/login',
      headers: { host: 'localhost:4000' },
      payload: { email: 'admin@test.com', password: 'Password123!' },
    });

    expect(r.statusCode).toBe(200);
    const cookies = r.headers['set-cookie'] as string[];
    expect(cookies.map((c) => c.split('=')[0])).toContain(COOKIE_ACCESS);
  });
});

describe('POST /auth/login/cli', () => {
  let ctx: TestCtx;

  beforeEach(async () => {
    ctx = await buildApp();
    await seedAdmin(ctx);
  });

  it('returns 200 with tokens in the body and sets NO cookies', async () => {
    const r = await ctx.app.inject({
      method: 'POST',
      url: '/auth/login/cli',
      headers: { host: HOST },
      payload: { email: 'admin@test.com', password: 'Password123!' },
    });

    expect(r.statusCode).toBe(200);
    const body = r.json() as { accessToken: string; refreshToken: string; expiresIn: number; tenantId: string };
    expect(body.accessToken).toBeTruthy();
    expect(body.refreshToken).toBeTruthy();
    expect(body.expiresIn).toBeGreaterThan(0);
    expect(body.tenantId).toBe(TENANT_ID);
    // The entire point of a dedicated route: never leak tokens via Set-Cookie.
    expect(r.headers['set-cookie']).toBeUndefined();
  });

  it('returns the shared credentials envelope on CLI wrong password', async () => {
    const r = await ctx.app.inject({
      method: 'POST',
      url: '/auth/login/cli',
      headers: { host: HOST },
      payload: { email: 'admin@test.com', password: 'WrongPassword99!' },
    });
    expect(r.statusCode).toBe(401);
    expect(r.json()).toMatchObject({ ok: false, error: { code: 'KB_AUTH_INVALID_CREDENTIALS' } });
  });

  it('returns 400 when email or password is missing', async () => {
    const r1 = await ctx.app.inject({
      method: 'POST', url: '/auth/login/cli', headers: { host: HOST }, payload: { password: 'x' },
    });
    expect(r1.statusCode).toBe(400);
    const r2 = await ctx.app.inject({
      method: 'POST', url: '/auth/login/cli', headers: { host: HOST }, payload: { email: 'admin@test.com' },
    });
    expect(r2.statusCode).toBe(400);
  });

  it('falls back to bootstrapTenantId when Host resolves no tenant (regression for `kb auth login` against a direct/local gateway URL)', async () => {
    const r = await ctx.app.inject({
      method: 'POST',
      url: '/auth/login/cli',
      headers: { host: 'localhost:4000' },
      payload: { email: 'admin@test.com', password: 'Password123!' },
    });

    expect(r.statusCode).toBe(200);
    const body = r.json() as { accessToken: string; tenantId: string };
    expect(body.accessToken).toBeTruthy();
    expect(body.tenantId).toBe(TENANT_ID);
  });
});

describe('POST /auth/refresh/cli', () => {
  let ctx: TestCtx;

  beforeEach(async () => {
    ctx = await buildApp();
    await seedAdmin(ctx);
  });

  it('rotates the token pair given a valid refreshToken in the body, sets no cookies', async () => {
    const login = await ctx.app.inject({
      method: 'POST',
      url: '/auth/login/cli',
      headers: { host: HOST },
      payload: { email: 'admin@test.com', password: 'Password123!' },
    });
    const { refreshToken } = login.json() as { refreshToken: string };

    const r = await ctx.app.inject({
      method: 'POST',
      url: '/auth/refresh/cli',
      headers: { host: HOST },
      payload: { refreshToken },
    });

    expect(r.statusCode).toBe(200);
    const body = r.json() as { accessToken: string; refreshToken: string; expiresIn: number };
    expect(body.accessToken).toBeTruthy();
    expect(body.refreshToken).toBeTruthy();
    expect(r.headers['set-cookie']).toBeUndefined();
  });

  it('returns 400 when refreshToken is missing', async () => {
    const r = await ctx.app.inject({
      method: 'POST', url: '/auth/refresh/cli', headers: { host: HOST }, payload: {},
    });
    expect(r.statusCode).toBe(400);
  });

  it('returns 401 for an invalid/tampered refreshToken', async () => {
    const r = await ctx.app.inject({
      method: 'POST', url: '/auth/refresh/cli', headers: { host: HOST }, payload: { refreshToken: 'not-a-jwt' },
    });
    expect(r.statusCode).toBe(401);
    expect(r.json()).toMatchObject({ ok: false, error: { code: 'KB_AUTH_REQUIRED' } });
  });
});

describe('Bearer user token (from /auth/login/cli) → POST /auth/register', () => {
  // This is the actual gap-closing scenario: a CLI-obtained user access
  // token, carried as a Bearer header (no cookie involved), must be able to
  // exercise the same MACHINE_REGISTER-gated endpoint a cookie-authed admin
  // can — see user-auth-middleware.ts's Bearer branch.
  let ctx: TestCtx;

  beforeEach(async () => {
    ctx = await buildApp();
    await seedAdmin(ctx);
  });

  it('an admin session token succeeds via Bearer, exactly like the cookie path', async () => {
    const login = await ctx.app.inject({
      method: 'POST',
      url: '/auth/login/cli',
      headers: { host: HOST },
      payload: { email: 'admin@test.com', password: 'Password123!' },
    });
    const { accessToken } = login.json() as { accessToken: string };

    const r = await ctx.app.inject({
      method: 'POST',
      url: '/auth/register',
      headers: { host: HOST, authorization: `Bearer ${accessToken}` },
      payload: { name: 'e2e-agent', capabilities: [] },
    });

    expect(r.statusCode).toBe(200);
    expect((r.json() as { clientId?: string }).clientId).toBeTruthy();
  });

  it('a non-admin member session token gets 403 (PDP denies MACHINE_REGISTER)', async () => {
    const inviteRes = await ctx.invites.createInvite({
      email: 'member@test.com',
      tenantId: TENANT_ID,
      groupId: 'tenant-member',
      createdBy: 'admin-1',
      ttlMs: HOUR,
    });
    const activateRes = await ctx.app.inject({
      method: 'POST',
      url: '/auth/activate',
      headers: { host: HOST },
      payload: { token: inviteRes.activationToken, password: 'MemberPass123!' },
    });
    expect(activateRes.statusCode).toBe(200);

    const login = await ctx.app.inject({
      method: 'POST',
      url: '/auth/login/cli',
      headers: { host: HOST },
      payload: { email: 'member@test.com', password: 'MemberPass123!' },
    });
    const { accessToken } = login.json() as { accessToken: string };

    const r = await ctx.app.inject({
      method: 'POST',
      url: '/auth/register',
      headers: { host: HOST, authorization: `Bearer ${accessToken}` },
      payload: { name: 'e2e-agent', capabilities: [] },
    });

    expect(r.statusCode).toBe(403);
  });
});

describe('POST /auth/logout', () => {
  let ctx: TestCtx;

  beforeEach(async () => {
    ctx = await buildApp();
    await seedAdmin(ctx);
  });

  it('clears session cookies on logout with valid CSRF', async () => {
    const { cookieHeader, csrfToken } = await loginAndGetCookies(ctx.app, {
      email: 'admin@test.com',
      password: 'Password123!',
    });

    const r = await ctx.app.inject({
      method: 'POST',
      url: '/auth/logout',
      headers: { host: HOST, cookie: cookieHeader, 'x-csrf-token': csrfToken },
    });

    expect(r.statusCode).toBe(200);
    const setCookies = r.headers['set-cookie'] as string[];
    // Cookies should be cleared (Max-Age=0).
    const cleared = setCookies.filter((c) => c.includes('Max-Age=0') || c.includes('Expires=Thu, 01 Jan 1970'));
    expect(cleared.length).toBeGreaterThanOrEqual(2);
  });

  it('returns 401 when no session cookie is present', async () => {
    const r = await ctx.app.inject({
      method: 'POST',
      url: '/auth/logout',
      headers: { host: HOST },
    });
    expect(r.statusCode).toBe(401);
  });

  it('returns 403 when CSRF token header is missing', async () => {
    const { cookieHeader } = await loginAndGetCookies(ctx.app, {
      email: 'admin@test.com',
      password: 'Password123!',
    });

    const r = await ctx.app.inject({
      method: 'POST',
      url: '/auth/logout',
      headers: { host: HOST, cookie: cookieHeader },
      // No x-csrf-token header.
    });

    expect(r.statusCode).toBe(403);
  });

  it('returns 403 when CSRF token does not match cookie value', async () => {
    const { cookieHeader } = await loginAndGetCookies(ctx.app, {
      email: 'admin@test.com',
      password: 'Password123!',
    });

    const r = await ctx.app.inject({
      method: 'POST',
      url: '/auth/logout',
      headers: { host: HOST, cookie: cookieHeader, 'x-csrf-token': 'wrong-csrf-token' },
    });

    expect(r.statusCode).toBe(403);
  });
});

describe('POST /auth/refresh (user cookie path)', () => {
  let ctx: TestCtx;

  beforeEach(async () => {
    ctx = await buildApp();
    await seedAdmin(ctx);
  });

  it('rotates the refresh cookie and issues new access + refresh cookies', async () => {
    const { cookieHeader } = await loginAndGetCookies(ctx.app, {
      email: 'admin@test.com',
      password: 'Password123!',
    });

    const r = await ctx.app.inject({
      method: 'POST',
      url: '/auth/refresh',
      headers: { host: HOST, cookie: cookieHeader },
    });

    expect(r.statusCode).toBe(200);
    const setCookies = r.headers['set-cookie'] as string[];
    const names = setCookies.map((c) => c.split('=')[0]);
    expect(names).toContain(COOKIE_ACCESS);
    expect(names).toContain(COOKIE_REFRESH);
  });

  it('falls back to machine body path (400) when no cookie and no body', async () => {
    // No cookie, no body → machine refresh path → 400 (missing refreshToken).
    const r = await ctx.app.inject({
      method: 'POST',
      url: '/auth/refresh',
      headers: { host: HOST },
    });
    expect(r.statusCode).toBe(400);
  });

  it('returns 401 for a tampered refresh cookie', async () => {
    const r = await ctx.app.inject({
      method: 'POST',
      url: '/auth/refresh',
      headers: { host: HOST, cookie: `${COOKIE_REFRESH}=not-a-jwt` },
    });
    expect(r.statusCode).toBe(401);
  });
});

describe('GET /auth/me (user context)', () => {
  let ctx: TestCtx;

  beforeEach(async () => {
    ctx = await buildApp();
    await seedAdmin(ctx);
  });

  it('returns userId, email, tenantId for cookie-auth user', async () => {
    const { cookieHeader } = await loginAndGetCookies(ctx.app, {
      email: 'admin@test.com',
      password: 'Password123!',
    });

    const r = await ctx.app.inject({
      method: 'GET',
      url: '/auth/me',
      headers: { host: HOST, cookie: cookieHeader },
    });

    expect(r.statusCode).toBe(200);
    expect(r.json()).toMatchObject({
      userId: 'admin-1',
      email: 'admin@test.com',
      tenantId: TENANT_ID,
    });
  });

  it('does NOT include role or group fields in the response', async () => {
    const { cookieHeader } = await loginAndGetCookies(ctx.app, {
      email: 'admin@test.com',
      password: 'Password123!',
    });

    const r = await ctx.app.inject({
      method: 'GET',
      url: '/auth/me',
      headers: { host: HOST, cookie: cookieHeader },
    });

    const body = r.json() as Record<string, unknown>;
    expect(body).not.toHaveProperty('role');
    expect(body).not.toHaveProperty('group');
    expect(body).not.toHaveProperty('groupId');
  });

  it('returns 401 without any auth', async () => {
    const r = await ctx.app.inject({
      method: 'GET',
      url: '/auth/me',
      headers: { host: HOST },
    });
    expect(r.statusCode).toBe(401);
  });

  // Studio-auth investigation (--local install mode, auth.enabled=false): the
  // running gateway falls back to the LOCAL_ADMIN machine context (B-023),
  // which /auth/me currently answers via its "machine Bearer" branch —
  // `{ userId, namespaceId }`, no `email`/`tenantId`. Studio's PublicUser type
  // (studio/app/src/auth/auth-provider.tsx) and router.tsx dereference
  // `auth.user.email`/`tenantId` directly with no defensive check, so a
  // `--local` install's Studio header/status bar breaks on this exact shape
  // mismatch. This test pins the CORRECT contract — /auth/me under local/
  // no-auth mode should still expose a Studio-compatible shape — and is
  // expected to FAIL against current code until that gap is closed.
  it('returns a Studio-compatible shape (email, tenantId) under local/no-auth mode', async () => {
    const localAdminApp = (await buildApp({ authDisabled: true })).app;

    const r = await localAdminApp.inject({
      method: 'GET',
      url: '/auth/me',
      headers: { host: HOST },
    });

    expect(r.statusCode).toBe(200);
    const body = r.json() as Record<string, unknown>;
    expect(body).toHaveProperty('email');
    expect(body).toHaveProperty('tenantId');
  });
});

describe('GET /auth/providers', () => {
  let ctx: TestCtx;

  beforeEach(async () => {
    ctx = await buildApp();
  });

  it('returns list of registered providers without auth', async () => {
    const r = await ctx.app.inject({
      method: 'GET',
      url: '/auth/providers',
      headers: { host: HOST },
    });

    expect(r.statusCode).toBe(200);
    const body = r.json() as { providers: { id: string; kind: string }[] };
    expect(body.providers).toContainEqual({ id: 'email-password', kind: 'password' });
  });
});

describe('GET /auth/permissions', () => {
  let ctx: TestCtx;

  beforeEach(async () => {
    ctx = await buildApp();
    await seedAdmin(ctx);
  });

  it('returns permissions list for authenticated admin', async () => {
    const { cookieHeader } = await loginAndGetCookies(ctx.app, {
      email: 'admin@test.com',
      password: 'Password123!',
    });

    const r = await ctx.app.inject({
      method: 'GET',
      url: '/auth/permissions',
      headers: { host: HOST, cookie: cookieHeader },
    });

    expect(r.statusCode).toBe(200);
    const body = r.json() as { permissions: string[] };
    expect(Array.isArray(body.permissions)).toBe(true);
    expect(body.permissions).toContain('users:read');
    expect(body.permissions).toContain('invites:write');
  });

  it('returns 401 without cookie', async () => {
    const r = await ctx.app.inject({
      method: 'GET',
      url: '/auth/permissions',
      headers: { host: HOST },
    });
    expect(r.statusCode).toBe(401);
  });

  it('returns all permissions for LOCAL_ADMIN machine context (auth disabled)', async () => {
    const localAdminApp = (await buildApp({ authDisabled: true })).app;

    const r = await localAdminApp.inject({
      method: 'GET',
      url: '/auth/permissions',
      headers: { host: HOST },
    });

    expect(r.statusCode).toBe(200);
    const body = r.json() as { permissions: string[] };
    expect(Array.isArray(body.permissions)).toBe(true);
    expect(body.permissions.length).toBeGreaterThan(0);
  });
});

describe('POST /auth/activate', () => {
  let ctx: TestCtx;

  beforeEach(async () => {
    ctx = await buildApp();
    await seedAdmin(ctx);
  });

  it('activates invite and auto-logs in (sets session cookies)', async () => {
    const inviteRes = await ctx.invites.createInvite({
      email: 'new@test.com',
      tenantId: TENANT_ID,
      groupId: 'tenant-member',
      createdBy: 'admin-1',
      ttlMs: HOUR,
    });

    const r = await ctx.app.inject({
      method: 'POST',
      url: '/auth/activate',
      headers: { host: HOST },
      payload: { token: inviteRes.activationToken, password: 'NewPassword123!' },
    });

    expect(r.statusCode).toBe(200);
    const cookies = r.headers['set-cookie'] as string[];
    expect(cookies.map((c) => c.split('=')[0])).toContain(COOKIE_ACCESS);
  });

  // case: S-105.3
  it('returns 422 for an unknown invite token', async () => {
    const r = await ctx.app.inject({
      method: 'POST',
      url: '/auth/activate',
      headers: { host: HOST },
      payload: { token: 'totally-invalid-token', password: 'NewPassword123!' },
    });
    expect(r.statusCode).toBe(422);
    expect(r.json()).toMatchObject({ ok: false, error: { code: 'KB_AUTH_INVALID_INVITE', message: 'The invitation is invalid, expired, or already used.', hint: expect.any(String) } });
  });

  // case: S-042.3
  it('returns the catalogued invite error when an activation token is reused', async () => {
    const invite = await ctx.invites.createInvite({
      email: 'reused@test.com', tenantId: TENANT_ID, groupId: 'tenant-member', createdBy: 'admin-1', ttlMs: HOUR,
    });
    const payload = { token: invite.activationToken, password: 'NewPassword123!' };
    const accepted = await ctx.app.inject({ method: 'POST', url: '/auth/activate', headers: { host: HOST }, payload });
    expect(accepted.statusCode).toBe(200);
    const reused = await ctx.app.inject({ method: 'POST', url: '/auth/activate', headers: { host: HOST }, payload });
    expect(reused.statusCode).toBe(422);
    expect(reused.json()).toMatchObject({ ok: false, error: { code: 'KB_AUTH_INVALID_INVITE', hint: expect.any(String) } });
  });

  it('returns 400 when token is missing', async () => {
    const r = await ctx.app.inject({
      method: 'POST',
      url: '/auth/activate',
      headers: { host: HOST },
      payload: { password: 'NewPassword123!' },
    });
    expect(r.statusCode).toBe(400);
  });
});

describe('POST /auth/password/change', () => {
  let ctx: TestCtx;

  beforeEach(async () => {
    ctx = await buildApp();
    await seedAdmin(ctx);
  });

  it('changes password successfully with CSRF', async () => {
    const { cookieHeader, csrfToken } = await loginAndGetCookies(ctx.app, {
      email: 'admin@test.com',
      password: 'Password123!',
    });

    const r = await ctx.app.inject({
      method: 'POST',
      url: '/auth/password/change',
      headers: { host: HOST, cookie: cookieHeader, 'x-csrf-token': csrfToken },
      payload: { currentPassword: 'Password123!', newPassword: 'NewPassword456!' },
    });

    expect(r.statusCode).toBe(200);
  });

  it('returns 400 when current password is wrong', async () => {
    const { cookieHeader, csrfToken } = await loginAndGetCookies(ctx.app, {
      email: 'admin@test.com',
      password: 'Password123!',
    });

    const r = await ctx.app.inject({
      method: 'POST',
      url: '/auth/password/change',
      headers: { host: HOST, cookie: cookieHeader, 'x-csrf-token': csrfToken },
      payload: { currentPassword: 'WrongCurrent!', newPassword: 'NewPassword456!' },
    });

    expect(r.statusCode).toBe(400);
    expect(r.json()).toMatchObject({
      ok: false,
      error: {
        code: 'KB_AUTH_INVALID_CURRENT_PASSWORD',
        message: 'Current password is incorrect',
        hint: 'Check the current password and retry the password change.',
      },
    });
  });

  it('returns 401 without session cookie', async () => {
    const r = await ctx.app.inject({
      method: 'POST',
      url: '/auth/password/change',
      headers: { host: HOST },
      payload: { currentPassword: 'Password123!', newPassword: 'NewPassword456!' },
    });
    expect(r.statusCode).toBe(401);
  });

  it('returns 403 without CSRF token', async () => {
    const { cookieHeader } = await loginAndGetCookies(ctx.app, {
      email: 'admin@test.com',
      password: 'Password123!',
    });

    const r = await ctx.app.inject({
      method: 'POST',
      url: '/auth/password/change',
      headers: { host: HOST, cookie: cookieHeader },
      payload: { currentPassword: 'Password123!', newPassword: 'NewPassword456!' },
    });

    expect(r.statusCode).toBe(403);
  });
});

describe('GET /auth/sessions', () => {
  let ctx: TestCtx;

  beforeEach(async () => {
    ctx = await buildApp();
    await seedAdmin(ctx);
  });

  it('returns session list including the current session', async () => {
    const { cookieHeader } = await loginAndGetCookies(ctx.app, {
      email: 'admin@test.com',
      password: 'Password123!',
    });

    const r = await ctx.app.inject({
      method: 'GET',
      url: '/auth/sessions',
      headers: { host: HOST, cookie: cookieHeader },
    });

    expect(r.statusCode).toBe(200);
    const body = r.json() as { sessions: { familyId: string; isCurrent: boolean }[] };
    expect(Array.isArray(body.sessions)).toBe(true);
    expect(body.sessions.length).toBeGreaterThanOrEqual(1);
    expect(body.sessions.some((s) => s.isCurrent)).toBe(true);
  });
});

describe('personal API tokens', () => {
  let ctx: TestCtx;
  beforeEach(async () => {
    ctx = await buildApp();
    await seedAdmin(ctx);
  });

  it('creates, lists, authenticates with, and owner-revokes an API token', async () => {
    const { cookieHeader, csrfToken } = await loginAndGetCookies(ctx.app, { email: 'admin@test.com', password: 'Password123!' });
    const created = await ctx.app.inject({
      method: 'POST', url: '/auth/tokens', headers: { host: HOST, cookie: cookieHeader, 'x-csrf-token': csrfToken },
      payload: { name: 'build agent' },
    });
    expect(created.statusCode).toBe(201);
    const body = created.json() as { token: string; tokenRecord: { tokenId: string; userId: string } };
    expect(body.token).toMatch(/^kbl_pat_/);
    expect(body.tokenRecord).not.toHaveProperty('expiresAt');

    const list = await ctx.app.inject({ method: 'GET', url: '/auth/tokens', headers: { host: HOST, cookie: cookieHeader } });
    expect(list.json()).toMatchObject({ tokens: [{ tokenId: body.tokenRecord.tokenId, userId: body.tokenRecord.userId, name: 'build agent' }] });
    expect(JSON.stringify(list.json())).not.toContain(body.token);

    expect((await ctx.app.inject({ method: 'GET', url: '/auth/me', headers: { host: HOST, authorization: `Bearer ${body.token}` } })).statusCode).toBe(200);
    const revoked = await ctx.app.inject({
      method: 'POST', url: `/auth/tokens/${body.tokenRecord.tokenId}/revoke`,
      headers: { host: HOST, cookie: cookieHeader, 'x-csrf-token': csrfToken },
    });
    expect(revoked.statusCode).toBe(200);
    expect((await ctx.app.inject({ method: 'GET', url: '/auth/me', headers: { host: HOST, authorization: `Bearer ${body.token}` } })).statusCode).toBe(401);
  });

  it('accepts a caller-selected token expiry and rejects expiry outside the supported range', async () => {
    const { cookieHeader, csrfToken } = await loginAndGetCookies(ctx.app, { email: 'admin@test.com', password: 'Password123!' });
    const createdAt = Date.now();
    const created = await ctx.app.inject({
      method: 'POST', url: '/auth/tokens', headers: { host: HOST, cookie: cookieHeader, 'x-csrf-token': csrfToken },
      payload: { name: 'expiring agent', expiresInDays: 90 },
    });
    expect(created.statusCode).toBe(201);
    const record = created.json().tokenRecord as { expiresAt: number };
    expect(record.expiresAt).toBeGreaterThanOrEqual(createdAt + 90 * 24 * HOUR - 5_000);
    expect(record.expiresAt).toBeLessThanOrEqual(createdAt + 90 * 24 * HOUR + 5_000);

    const invalid = await ctx.app.inject({
      method: 'POST', url: '/auth/tokens', headers: { host: HOST, cookie: cookieHeader, 'x-csrf-token': csrfToken },
      payload: { name: 'invalid expiry', expiresInDays: 366 },
    });
    expect(invalid.statusCode).toBe(400);
    expect(invalid.json()).toMatchObject({ ok: false, error: { code: 'KB_RUNTIME_INPUT_INVALID', correlationId: expect.any(String) } });
  });

  it('does not let an owner revoke another user API token', async () => {
    const { cookieHeader, csrfToken } = await loginAndGetCookies(ctx.app, { email: 'admin@test.com', password: 'Password123!' });
    const other = await ctx.apiTokens.create({ userId: 'other-user', tenantId: TENANT_ID, name: 'other CI', ttlMs: HOUR });
    const response = await ctx.app.inject({
      method: 'POST', url: `/auth/tokens/${other.record.tokenId}/revoke`,
      headers: { host: HOST, cookie: cookieHeader, 'x-csrf-token': csrfToken },
    });
    expect(response.statusCode).toBe(404);
    expect(await ctx.apiTokens.findActive(other.token)).not.toBeNull();
  });
});

describe('POST /auth/sessions/:fam/revoke', () => {
  let ctx: TestCtx;

  beforeEach(async () => {
    ctx = await buildApp();
    await seedAdmin(ctx);
  });

  it('revokes a target session family', async () => {
    const { cookieHeader, csrfToken } = await loginAndGetCookies(ctx.app, {
      email: 'admin@test.com',
      password: 'Password123!',
    });

    // Create a second session to revoke.
    const secondSession = await ctx.sessions.createSession({
      userId: 'admin-1',
      tenantId: TENANT_ID,
      deviceCtx: {},
    });

    const r = await ctx.app.inject({
      method: 'POST',
      url: `/auth/sessions/${secondSession.familyId}/revoke`,
      headers: { host: HOST, cookie: cookieHeader, 'x-csrf-token': csrfToken },
    });

    expect(r.statusCode).toBe(200);
  });

  it('returns 403 without CSRF token', async () => {
    const { cookieHeader } = await loginAndGetCookies(ctx.app, {
      email: 'admin@test.com',
      password: 'Password123!',
    });

    const r = await ctx.app.inject({
      method: 'POST',
      url: '/auth/sessions/some-fam/revoke',
      headers: { host: HOST, cookie: cookieHeader },
    });

    expect(r.statusCode).toBe(403);
  });
});

describe('POST /auth/sessions/revoke-all', () => {
  let ctx: TestCtx;

  beforeEach(async () => {
    ctx = await buildApp();
    await seedAdmin(ctx);
  });

  it('revokes all sessions except current', async () => {
    const { cookieHeader, csrfToken } = await loginAndGetCookies(ctx.app, {
      email: 'admin@test.com',
      password: 'Password123!',
    });

    // Create extra sessions to be revoked.
    await ctx.sessions.createSession({ userId: 'admin-1', tenantId: TENANT_ID, deviceCtx: {} });
    await ctx.sessions.createSession({ userId: 'admin-1', tenantId: TENANT_ID, deviceCtx: {} });

    const r = await ctx.app.inject({
      method: 'POST',
      url: '/auth/sessions/revoke-all',
      headers: { host: HOST, cookie: cookieHeader, 'x-csrf-token': csrfToken },
    });

    expect(r.statusCode).toBe(200);
  });
});

describe('POST /auth/invites (admin only)', () => {
  let ctx: TestCtx;

  beforeEach(async () => {
    ctx = await buildApp();
    await seedAdmin(ctx);
  });

  it('creates an invite and returns activationUrl (admin)', async () => {
    const { cookieHeader, csrfToken } = await loginAndGetCookies(ctx.app, {
      email: 'admin@test.com',
      password: 'Password123!',
    });

    const r = await ctx.app.inject({
      method: 'POST',
      url: '/auth/invites',
      headers: { host: HOST, cookie: cookieHeader, 'x-csrf-token': csrfToken },
      payload: { email: 'newguy@test.com', role: 'member' },
    });

    expect(r.statusCode).toBe(201);
    const body = r.json() as { activationUrl: string; inviteId: string };
    expect(body.activationUrl).toContain('/activate/');
    expect(body.inviteId).toBeTruthy();
    const created = await ctx.invites.findById(body.inviteId);
    expect(created?.expiresAt).toBeGreaterThanOrEqual(Date.now() + 7 * 24 * HOUR - 5_000);
    expect(created?.expiresAt).toBeLessThanOrEqual(Date.now() + 7 * 24 * HOUR + 5_000);
  });

  it('returns 403 for a non-admin member', async () => {
    // Create a member via invite flow (they get a real bcrypt hash via activate).
    const inviteRes = await ctx.invites.createInvite({
      email: 'member@test.com',
      tenantId: TENANT_ID,
      groupId: 'tenant-member',
      createdBy: 'admin-1',
      ttlMs: HOUR,
    });
    const activateRes = await ctx.app.inject({
      method: 'POST',
      url: '/auth/activate',
      headers: { host: HOST },
      payload: { token: inviteRes.activationToken, password: 'MemberPass123!' },
    });
    expect(activateRes.statusCode).toBe(200);

    // Extract member cookies.
    const memberCookies = (activateRes.headers['set-cookie'] as string[])
      .map((c) => c.split(';')[0] ?? '')
      .join('; ');
    const csrfCookieStr = (activateRes.headers['set-cookie'] as string[])
      .find((c) => c.startsWith(COOKIE_CSRF + '='));
    const memberCsrf = csrfCookieStr
      ? (csrfCookieStr.split(';')[0] ?? '').split('=').slice(1).join('=')
      : '';

    const r = await ctx.app.inject({
      method: 'POST',
      url: '/auth/invites',
      headers: { host: HOST, cookie: memberCookies, 'x-csrf-token': memberCsrf },
      payload: { email: 'another@test.com', role: 'member' },
    });

    expect(r.statusCode).toBe(403);
  });
});

describe('GET /auth/invites (admin only)', () => {
  let ctx: TestCtx;

  beforeEach(async () => {
    ctx = await buildApp();
    await seedAdmin(ctx);
  });

  it('returns invite list for admin', async () => {
    const { cookieHeader } = await loginAndGetCookies(ctx.app, {
      email: 'admin@test.com',
      password: 'Password123!',
    });

    const r = await ctx.app.inject({
      method: 'GET',
      url: '/auth/invites',
      headers: { host: HOST, cookie: cookieHeader },
    });

    expect(r.statusCode).toBe(200);
    const body = r.json() as { invites: unknown[] };
    expect(Array.isArray(body.invites)).toBe(true);
  });
});

describe('GET /auth/users (admin only)', () => {
  let ctx: TestCtx;

  beforeEach(async () => {
    ctx = await buildApp();
    await seedAdmin(ctx);
  });

  it('returns user list including admin for tenant', async () => {
    const { cookieHeader } = await loginAndGetCookies(ctx.app, {
      email: 'admin@test.com',
      password: 'Password123!',
    });

    const r = await ctx.app.inject({
      method: 'GET',
      url: '/auth/users',
      headers: { host: HOST, cookie: cookieHeader },
    });

    expect(r.statusCode).toBe(200);
    const body = r.json() as { users: { userId: string }[] };
    expect(Array.isArray(body.users)).toBe(true);
    expect(body.users.some((u) => u.userId === 'admin-1')).toBe(true);
    expect(body.users.find((u) => u.userId === 'admin-1')).toHaveProperty('role', 'admin');
  });

  // case: S-005.5
  it('returns the shared authorization envelope with the missing permission', async () => {
    const invite = await ctx.invites.createInvite({
      email: 'member@test.com', tenantId: TENANT_ID, groupId: 'tenant-member',
      createdBy: 'admin-1', ttlMs: HOUR,
    });
    await ctx.app.inject({ method: 'POST', url: '/auth/activate', headers: { host: HOST },
      payload: { token: invite.activationToken, password: 'MemberPass123!' } });
    const login = await ctx.app.inject({ method: 'POST', url: '/auth/login/cli', headers: { host: HOST },
      payload: { email: 'member@test.com', password: 'MemberPass123!' } });
    const { accessToken } = login.json() as { accessToken: string };

    const response = await ctx.app.inject({ method: 'GET', url: '/auth/users',
      headers: { host: HOST, authorization: `Bearer ${accessToken}` } });

    expect(response.statusCode).toBe(403);
    expect(response.json()).toMatchObject({ ok: false, error: {
      code: 'KB_AUTH_FORBIDDEN', message: 'Permission denied: users:read', hint: expect.any(String),
      details: { permission: 'users:read' },
    } });
  });

  it('changes a tenant user role and updates permissions on their next request', async () => {
    await ctx.users.create({ userId: 'role-target', tenantId: TENANT_ID, email: 'member@test.com', status: 'active' });
    await ctx.memberships.addMembership({ userId: 'role-target', tenantId: TENANT_ID, groupId: 'tenant-member' });
    await ctx.credentials.setCredential({ userId: 'role-target', providerId: 'email-password', hash: await bcryptjs.hash('MemberPass123!', 4) });
    const admin = await loginAndGetCookies(ctx.app, { email: 'admin@test.com', password: 'Password123!' });
    const changed = await ctx.app.inject({
      method: 'POST', url: '/auth/users/role-target/role',
      headers: { host: HOST, cookie: admin.cookieHeader, 'x-csrf-token': admin.csrfToken },
      payload: { role: 'admin' },
    });
    expect(changed.statusCode).toBe(200);
    const listed = await ctx.app.inject({ method: 'GET', url: '/auth/users', headers: { host: HOST, cookie: admin.cookieHeader } });
    expect(listed.json().users.find((user: { userId: string }) => user.userId === 'role-target').role).toBe('admin');
    const member = await loginAndGetCookies(ctx.app, { email: 'member@test.com', password: 'MemberPass123!' });
    const permissions = await ctx.app.inject({ method: 'GET', url: '/auth/permissions', headers: { host: HOST, cookie: member.cookieHeader } });
    expect(permissions.json().permissions).toContain('users:read');
  });

  it('prevents disabling or demoting the final active administrator', async () => {
    const admin = await loginAndGetCookies(ctx.app, { email: 'admin@test.com', password: 'Password123!' });
    const disable = await ctx.app.inject({ method: 'POST', url: '/auth/users/admin-1/disable',
      headers: { host: HOST, cookie: admin.cookieHeader, 'x-csrf-token': admin.csrfToken } });
    expect(disable.statusCode).toBe(409);
    expect(disable.json()).toMatchObject({ ok: false, error: { code: 'KB_AUTH_LAST_ADMIN', hint: expect.any(String) } });
    const demote = await ctx.app.inject({ method: 'POST', url: '/auth/users/admin-1/role',
      headers: { host: HOST, cookie: admin.cookieHeader, 'x-csrf-token': admin.csrfToken }, payload: { role: 'member' } });
    expect(demote.statusCode).toBe(409);
    expect(demote.json()).toMatchObject({ ok: false, error: { code: 'KB_AUTH_LAST_ADMIN', hint: expect.any(String) } });
    expect((await ctx.users.getById('admin-1'))?.status).toBe('active');
  });

  it('serializes concurrent demotions of the last two admins so exactly one remains active', async () => {
    await seedAdmin(ctx, { userId: 'admin-2', email: 'admin2@test.com', password: 'Password123!' });
    const admin = await loginAndGetCookies(ctx.app, { email: 'admin@test.com', password: 'Password123!' });
    let permissionChecks = 0;
    let releaseChecks!: () => void;
    const bothChecksStarted = new Promise<void>((resolve) => { releaseChecks = resolve; });
    const checkPermission = ctx.pdp.check.bind(ctx.pdp);
    ctx.pdp.check = async (principal, permission) => {
      if (permission === PERMISSIONS.USERS_WRITE) {
        permissionChecks += 1;
        if (permissionChecks === 2) {releaseChecks();}
        if (permissionChecks <= 2) {await bothChecksStarted;}
      }
      return checkPermission(principal, permission);
    };
    const [firstResult, secondResult] = await Promise.all([
      ctx.app.inject({ method: 'POST', url: '/auth/users/admin-1/role',
        headers: { host: HOST, cookie: admin.cookieHeader, 'x-csrf-token': admin.csrfToken }, payload: { role: 'member' } }),
      ctx.app.inject({ method: 'POST', url: '/auth/users/admin-2/role',
        headers: { host: HOST, cookie: admin.cookieHeader, 'x-csrf-token': admin.csrfToken }, payload: { role: 'member' } }),
    ]);

    expect([firstResult.statusCode, secondResult.statusCode].sort()).toEqual([200, 409]);
    const memberships = await ctx.memberships.listByTenant(TENANT_ID);
    const adminMemberships = memberships.filter((membership) => membership.groupId === 'tenant-admin');
    const activeAdmins = await Promise.all(adminMemberships.map(async ({ userId }) => ctx.users.getById(userId)));
    expect(activeAdmins.filter((user) => user?.status === 'active')).toHaveLength(1);
  });
});

describe('POST /auth/users/:id/disable (admin only, CD-1)', () => {
  let ctx: TestCtx;

  beforeEach(async () => {
    ctx = await buildApp();
    await seedAdmin(ctx);
  });

  it('disables user and their next request with valid access token returns 401 (CD-1)', async () => {
    // Create a member via invite + activate (they get real cookies).
    const inviteRes = await ctx.invites.createInvite({
      email: 'victim@test.com',
      tenantId: TENANT_ID,
      groupId: 'tenant-member',
      createdBy: 'admin-1',
      ttlMs: HOUR,
    });

    const activateRes = await ctx.app.inject({
      method: 'POST',
      url: '/auth/activate',
      headers: { host: HOST },
      payload: { token: inviteRes.activationToken, password: 'VictimPass123!' },
    });
    expect(activateRes.statusCode).toBe(200);

    const victimCookies = (activateRes.headers['set-cookie'] as string[])
      .map((c) => c.split(';')[0])
      .join('; ');

    // Victim can access /auth/me before disable.
    const beforeDisable = await ctx.app.inject({
      method: 'GET',
      url: '/auth/me',
      headers: { host: HOST, cookie: victimCookies },
    });
    expect(beforeDisable.statusCode).toBe(200);
    const victimId = (beforeDisable.json() as { userId: string }).userId;

    // Admin disables victim.
    const { cookieHeader: adminCookies, csrfToken: adminCsrf } = await loginAndGetCookies(ctx.app, {
      email: 'admin@test.com',
      password: 'Password123!',
    });

    const disableRes = await ctx.app.inject({
      method: 'POST',
      url: `/auth/users/${victimId}/disable`,
      headers: { host: HOST, cookie: adminCookies, 'x-csrf-token': adminCsrf },
    });
    expect(disableRes.statusCode).toBe(200);

    // CD-1: victim's next request with still-valid access token → 401.
    const afterDisable = await ctx.app.inject({
      method: 'GET',
      url: '/auth/me',
      headers: { host: HOST, cookie: victimCookies },
    });
    expect(afterDisable.statusCode).toBe(401);
  });
});

describe('POST /auth/users/:id/enable (admin only)', () => {
  let ctx: TestCtx;

  beforeEach(async () => {
    ctx = await buildApp();
    await seedAdmin(ctx);
  });

  it('re-enables a disabled user', async () => {
    await ctx.users.create({
      userId: 'sleeper',
      tenantId: TENANT_ID,
      email: 'sleeper@test.com',
      status: 'disabled',
    });

    const { cookieHeader: adminCookies, csrfToken: adminCsrf } = await loginAndGetCookies(ctx.app, {
      email: 'admin@test.com',
      password: 'Password123!',
    });

    const r = await ctx.app.inject({
      method: 'POST',
      url: '/auth/users/sleeper/enable',
      headers: { host: HOST, cookie: adminCookies, 'x-csrf-token': adminCsrf },
    });
    expect(r.statusCode).toBe(200);

    const updated = await ctx.users.getById('sleeper');
    expect(updated?.status).toBe('active');
  });
});

describe('POST /auth/invites/:id/revoke (admin only)', () => {
  let ctx: TestCtx;

  beforeEach(async () => {
    ctx = await buildApp();
    await seedAdmin(ctx);
  });

  it('revokes a pending invite', async () => {
    const { inviteId } = await ctx.invites.createInvite({
      email: 'tobrevoked@test.com',
      tenantId: TENANT_ID,
      groupId: 'tenant-member',
      createdBy: 'admin-1',
      ttlMs: HOUR,
    });

    const { cookieHeader, csrfToken } = await loginAndGetCookies(ctx.app, {
      email: 'admin@test.com',
      password: 'Password123!',
    });

    const r = await ctx.app.inject({
      method: 'POST',
      url: `/auth/invites/${inviteId}/revoke`,
      headers: { host: HOST, cookie: cookieHeader, 'x-csrf-token': csrfToken },
    });

    expect(r.statusCode).toBe(200);
  });
});

// ── Cross-tenant isolation on admin endpoints (IDOR guard) ──────────────────────
//
// requirePermission proves "you are an admin of YOUR tenant" — it does NOT prove
// the target resource belongs to that tenant. Without an explicit ownership check
// an admin of tenant A could disable/enable any user, or revoke any invite, in
// tenant B just by guessing the id. These tests assert the guard: cross-tenant
// targets return 404 AND the target is left untouched.
describe('admin endpoints — cross-tenant isolation', () => {
  const OTHER_TENANT = 'other-cloud';
  let ctx: TestCtx;

  beforeEach(async () => {
    ctx = await buildApp();
    await seedAdmin(ctx); // admin-1 in kb-cloud
  });

  async function adminAuth() {
    return loginAndGetCookies(ctx.app, { email: 'admin@test.com', password: 'Password123!' });
  }

  it('disable: admin of tenant A cannot disable a user in tenant B → 404, target untouched', async () => {
    await ctx.users.create({
      userId: 'foreign-user',
      tenantId: OTHER_TENANT,
      email: 'foreigner@other.com',
      status: 'active',
    });

    const { cookieHeader, csrfToken } = await adminAuth();
    const r = await ctx.app.inject({
      method: 'POST',
      url: '/auth/users/foreign-user/disable',
      headers: { host: HOST, cookie: cookieHeader, 'x-csrf-token': csrfToken },
    });

    expect(r.statusCode).toBe(404);
    // Critical: the cross-tenant user must remain active.
    const after = await ctx.users.getById('foreign-user');
    expect(after?.status).toBe('active');
  });

  it('enable: admin of tenant A cannot enable a user in tenant B → 404, target untouched', async () => {
    await ctx.users.create({
      userId: 'foreign-disabled',
      tenantId: OTHER_TENANT,
      email: 'sleeper@other.com',
      status: 'disabled',
    });

    const { cookieHeader, csrfToken } = await adminAuth();
    const r = await ctx.app.inject({
      method: 'POST',
      url: '/auth/users/foreign-disabled/enable',
      headers: { host: HOST, cookie: cookieHeader, 'x-csrf-token': csrfToken },
    });

    expect(r.statusCode).toBe(404);
    const after = await ctx.users.getById('foreign-disabled');
    expect(after?.status).toBe('disabled');
  });

  it('invite revoke: admin of tenant A cannot revoke an invite in tenant B → 404, invite stays active', async () => {
    const { inviteId, activationToken } = await ctx.invites.createInvite({
      email: 'invitee@other.com',
      tenantId: OTHER_TENANT,
      groupId: 'tenant-member',
      createdBy: 'someone-else',
      ttlMs: HOUR,
    });

    const { cookieHeader, csrfToken } = await adminAuth();
    const r = await ctx.app.inject({
      method: 'POST',
      url: `/auth/invites/${inviteId}/revoke`,
      headers: { host: HOST, cookie: cookieHeader, 'x-csrf-token': csrfToken },
    });

    expect(r.statusCode).toBe(404);
    // Critical: the cross-tenant invite must remain usable.
    expect((await ctx.invites.findByToken(activationToken)).kind).toBe('ok');
  });

  it('disable: returns 404 for a non-existent user id (no existence leak)', async () => {
    const { cookieHeader, csrfToken } = await adminAuth();
    const r = await ctx.app.inject({
      method: 'POST',
      url: '/auth/users/ghost/disable',
      headers: { host: HOST, cookie: cookieHeader, 'x-csrf-token': csrfToken },
    });
    expect(r.statusCode).toBe(404);
  });
});

// ── Login rate-limit gate runs BEFORE bcrypt (CPU-exhaustion DoS guard) ──────────
//
// The handler peeks the failure counter before invoking the provider (bcrypt).
// Once the per-email window is exhausted, further attempts are rejected with 429
// without running bcrypt. Only FAILED attempts increment the counter, so a
// successful login never throttles a legitimate user.
describe('POST /auth/login — rate limiting', () => {
  let ctx: TestCtx;

  beforeEach(async () => {
    ctx = await buildApp({
      rateLimit: { loginPerIpPerMinute: 100, loginPerEmailPerMinute: 3 },
    });
    await seedAdmin(ctx);
  });

  it('returns 429 once the per-email failure window is exhausted', async () => {
    const attempt = (pw: string) =>
      ctx.app.inject({
        method: 'POST',
        url: '/auth/login',
        headers: { host: HOST },
        payload: { email: 'admin@test.com', password: pw },
      });

    // 3 failed attempts are allowed (each 401), the 4th is throttled.
    expect((await attempt('wrong-1!')).statusCode).toBe(401);
    expect((await attempt('wrong-2!')).statusCode).toBe(401);
    expect((await attempt('wrong-3!')).statusCode).toBe(401);

    const throttled = await attempt('wrong-4!');
    expect(throttled.statusCode).toBe(429);
    expect(throttled.headers['retry-after']).toBeTruthy();
  });

  it('does not throttle a successful login (only failures count)', async () => {
    // Two failures, then a correct login: must still succeed (counter only
    // tracks failures and the success path never increments it).
    await ctx.app.inject({
      method: 'POST', url: '/auth/login', headers: { host: HOST },
      payload: { email: 'admin@test.com', password: 'wrong-1!' },
    });
    await ctx.app.inject({
      method: 'POST', url: '/auth/login', headers: { host: HOST },
      payload: { email: 'admin@test.com', password: 'wrong-2!' },
    });

    const ok = await ctx.app.inject({
      method: 'POST', url: '/auth/login', headers: { host: HOST },
      payload: { email: 'admin@test.com', password: 'Password123!' },
    });
    expect(ok.statusCode).toBe(200);
  });
});

describe('admin password reset links', () => {
  it('creates a one-time tenant-scoped reset link and accepts it once', async () => {
    const ctx = await buildApp();
    await seedAdmin(ctx);
    await ctx.users.create({ userId: 'member-reset', tenantId: TENANT_ID, email: 'member-reset@test.com', status: 'active' });
    await ctx.credentials.setCredential({ userId: 'member-reset', providerId: 'email-password', hash: await bcryptjs.hash('OldPassword123!', 4) });
    const admin = await ctx.app.inject({ method: 'POST', url: '/auth/login/cli', headers: { host: HOST }, payload: { email: 'admin@test.com', password: 'Password123!' } });
    const adminToken = (admin.json() as { accessToken: string }).accessToken;
    const issuedAt = Date.now();
    const issued = await ctx.app.inject({
      method: 'POST', url: '/auth/users/member-reset/password-reset-link',
      headers: { host: HOST, authorization: `Bearer ${adminToken}` },
    });
    expect(issued.statusCode).toBe(201);
    const { resetUrl, expiresAt } = issued.json() as { resetUrl: string; expiresAt: number };
    expect(expiresAt).toBeGreaterThanOrEqual(issuedAt + 24 * HOUR - 5_000);
    expect(expiresAt).toBeLessThanOrEqual(issuedAt + 24 * HOUR + 5_000);
    const token = new URL(resetUrl).pathname.split('/').at(-1);
    expect(token).toBeTruthy();

    const applied = await ctx.app.inject({ method: 'POST', url: '/auth/password/reset', headers: { host: HOST }, payload: { token, newPassword: 'NewPassword123!' } });
    expect(applied.statusCode).toBe(200);
    const reused = await ctx.app.inject({ method: 'POST', url: '/auth/password/reset', headers: { host: HOST }, payload: { token, newPassword: 'OtherPassword123!' } });
    expect(reused.statusCode).toBe(400);
    expect(reused.json()).toMatchObject({ ok: false, error: { code: 'KB_AUTH_INVALID_PASSWORD_RESET', hint: expect.any(String) } });
    const login = await ctx.app.inject({ method: 'POST', url: '/auth/login', headers: { host: HOST }, payload: { email: 'member-reset@test.com', password: 'NewPassword123!' } });
    expect(login.statusCode).toBe(200);
    await ctx.app.close();
  });
});

describe('POST /auth/logout/cli', () => {
  it('revokes the refresh family before the CLI removes its local session', async () => {
    const ctx = await buildApp();
    await seedAdmin(ctx);
    const login = await ctx.app.inject({ method: 'POST', url: '/auth/login/cli', headers: { host: HOST }, payload: { email: 'admin@test.com', password: 'Password123!' } });
    const refreshToken = (login.json() as { refreshToken: string }).refreshToken;
    expect((await ctx.app.inject({ method: 'POST', url: '/auth/logout/cli', headers: { host: HOST }, payload: { refreshToken } })).statusCode).toBe(200);
    const refresh = await ctx.app.inject({ method: 'POST', url: '/auth/refresh/cli', headers: { host: HOST }, payload: { refreshToken } });
    expect(refresh.statusCode).toBe(401);
    await ctx.app.close();
  });
});
