import { describe, it, expect, beforeEach } from 'vitest';
import type { ICache } from '@kb-labs/core-platform';
import { AuthService } from '../service.js';
import { signRefreshToken, signUserRefreshToken } from '../jwt.js';

const jwtConfig = { secret: 'test-secret-at-least-32-chars-long!!' };

function createMemCache() {
  const store = new Map<string, unknown>();
  return {
    async get<T>(key: string): Promise<T | null> {
      return (store.get(key) as T) ?? null;
    },
    async set<T>(key: string, value: T, _ttl?: number): Promise<void> {
      store.set(key, value);
    },
    async delete(key: string): Promise<void> {
      store.delete(key);
    },
    async clear(): Promise<void> {
      store.clear();
    },
  };
}

describe('AuthService', () => {
  let cache: ReturnType<typeof createMemCache>;
  let service: AuthService;

  beforeEach(() => {
    cache = createMemCache();
    service = new AuthService(cache as any, jwtConfig);
  });

  describe('register', () => {
    it('returns clientId, clientSecret, hostId, and server-generated namespaceId', async () => {
      const result = await service.register({
        name: 'MacBook',
        capabilities: ['filesystem'],
      });
      expect(result.clientId).toMatch(/^clt_/);
      expect(result.clientSecret).toMatch(/^cs_/);
      expect(result.hostId).toMatch(/^host_/);
      expect(result.namespaceId).toMatch(/^ns_/);
    });

    it('generates a unique namespaceId per registration (no impersonation)', async () => {
      const a = await service.register({ name: 'A', capabilities: [] });
      const b = await service.register({ name: 'B', capabilities: [] });
      expect(a.namespaceId).not.toBe(b.namespaceId);
    });

    it('saves public key when provided', async () => {
      const result = await service.register({
        name: 'laptop',
        capabilities: [],
        publicKey: 'my-pub-key',
      });
      const stored = await cache.get<string>(`auth:publickey:${result.hostId}`);
      expect(stored).toBe('my-pub-key');
    });
  });

  describe('issueTokens', () => {
    it('returns token pair on valid credentials', async () => {
      const { clientId, clientSecret } = await service.register({
        name: 'laptop',
        capabilities: [],
      });

      const tokens = await service.issueTokens(clientId, clientSecret);
      expect(tokens).not.toBeNull();
      expect(tokens!.accessToken).toBeTruthy();
      expect(tokens!.refreshToken).toBeTruthy();
      expect(tokens!.tokenType).toBe('Bearer');
      expect(tokens!.expiresIn).toBe(15 * 60);
    });

    it('returns null on wrong secret', async () => {
      const { clientId } = await service.register({
        name: 'laptop',
        capabilities: [],
      });

      const tokens = await service.issueTokens(clientId, 'wrong-secret');
      expect(tokens).toBeNull();
    });

    it('returns null for unknown clientId', async () => {
      const tokens = await service.issueTokens('clt_unknown', 'any');
      expect(tokens).toBeNull();
    });
  });

  describe('verify', () => {
    it('returns AuthContext for valid access token', async () => {
      const { clientId, clientSecret } = await service.register({
        name: 'laptop',
        capabilities: [],
      });
      const tokens = await service.issueTokens(clientId, clientSecret);

      const ctx = await service.verify(tokens!.accessToken);
      expect(ctx.ok).toBe(true);
      if (ctx.ok) {
        expect(ctx.payload.type).toBe('machine');
        expect(ctx.payload.namespaceId).toMatch(/^ns_/);
      }
    });

    it('returns null for garbage token', async () => {
      const ctx = await service.verify('not-a-jwt');
      expect(ctx).toEqual({ ok: false, reason: 'invalid' });
    });
  });

  describe('me', () => {
    it('returns profile with hostId and namespaceId for registered client', async () => {
      const { hostId, namespaceId } = await service.register({ name: 'laptop', capabilities: [] });

      const profile = await service.me(hostId);
      expect(profile).not.toBeNull();
      expect(profile!.hostId).toBe(hostId);
      expect(profile!.namespaceId).toBe(namespaceId);
    });

    it('returns handle when set during registration', async () => {
      const { hostId } = await service.register({ name: 'laptop', capabilities: [], handle: 'kirill' });

      const profile = await service.me(hostId);
      expect(profile!.handle).toBe('kirill');
    });

    it('returns null for unknown hostId', async () => {
      const profile = await service.me('host_unknown');
      expect(profile).toBeNull();
    });
  });

  describe('refreshTokens', () => {
    it('issues new token pair on valid refresh token', async () => {
      const { clientId, clientSecret } = await service.register({
        name: 'laptop',
        capabilities: [],
      });
      const first = await service.issueTokens(clientId, clientSecret);

      const second = await service.refreshTokens(first!.refreshToken);
      expect(second.ok).toBe(true);
      if (second.ok) {
        expect(second.payload.accessToken).toBeTruthy();
        expect(second.payload.refreshToken).not.toBe(first!.refreshToken); // rotated
      }
    });

    it('rejects the same refresh token twice (rotation)', async () => {
      const { clientId, clientSecret } = await service.register({
        name: 'laptop',
        capabilities: [],
      });
      const first = await service.issueTokens(clientId, clientSecret);

      await service.refreshTokens(first!.refreshToken);
      const third = await service.refreshTokens(first!.refreshToken);
      expect(third).toEqual({ ok: false, reason: 'revoked' });
    });

    it('returns null for garbage refresh token', async () => {
      const result = await service.refreshTokens('not-a-jwt');
      expect(result).toEqual({ ok: false, reason: 'invalid' });
    });
  });

  it('revokes a machine refresh token during CLI logout', async () => {
    const { clientId, clientSecret } = await service.register({ name: 'laptop', capabilities: [] });
    const tokens = await service.issueTokens(clientId, clientSecret);
    expect(tokens).not.toBeNull();
    expect(await service.revokeRefreshToken(tokens!.refreshToken)).toBe(true);
    expect(await service.refreshTokens(tokens!.refreshToken)).toEqual({ ok: false, reason: 'revoked' });
  });

  it('does not look up a user refresh token in the machine refresh-token cache', async () => {
    const userRefreshToken = await signUserRefreshToken({
      userId: 'user-1',
      tenantId: 'tenant-1',
      familyId: 'family-1',
      ttlSec: 3600,
    }, jwtConfig);
    const machineCache = {
      get: async () => { throw new Error('machine refresh-token cache unavailable'); },
      set: async () => {},
      delete: async () => {},
      clear: async () => {},
    } as unknown as ICache;
    const machineAuth = new AuthService(machineCache, jwtConfig);

    await expect(machineAuth.revokeRefreshToken(userRefreshToken)).resolves.toBe(false);
  });

  it('classifies machine and user refresh tokens by explicit kind', async () => {
    const machineRefreshToken = await signRefreshToken('host-1', jwtConfig);
    const userRefreshToken = await signUserRefreshToken({
      userId: 'user-1', tenantId: 'tenant-1', familyId: 'family-1', ttlSec: 3600,
    }, jwtConfig);

    await expect(service.classifyRefreshToken(machineRefreshToken)).resolves.toEqual({
      ok: true, payload: { kind: 'machine', hostId: 'host-1' },
    });
    await expect(service.classifyRefreshToken(userRefreshToken)).resolves.toMatchObject({
      ok: true, payload: { kind: 'user', userId: 'user-1', tenantId: 'tenant-1', familyId: 'family-1' },
    });
  });
});
