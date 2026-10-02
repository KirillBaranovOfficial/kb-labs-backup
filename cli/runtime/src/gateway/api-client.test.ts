import { afterEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({ session: null as null | { gatewayUrl: string; accessToken: string; refreshToken: string; expiresAt: number }, refresh: vi.fn() }));
vi.mock('./session.js', () => ({
  SessionManager: class {
    async load() { return mocks.session; }
    isExpired() { return false; }
    async refresh() { return mocks.refresh(); }
  },
}));
vi.mock('./credentials.js', () => ({
  CredentialsManager: class { async load() { return null; } },
}));

import { gatewayApiFetch } from './api-client.js';

describe('gatewayApiFetch auth mapping', () => {
  afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); mocks.session = null; mocks.refresh.mockReset(); });

  // case: S-041.7
  it('maps a 401 to the catalog required-auth envelope', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('', { status: 401 })));
    await expect(gatewayApiFetch('http://127.0.0.1:4000/api')).rejects.toMatchObject({
      envelope: { code: 'KB_AUTH_REQUIRED', hint: 'Run "kb auth login --gateway-url <url> --email <email> --password <password>".' },
    });
  });

  // case: S-041.9
  it('maps the permission reported by a 403 response without inventing a role', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({
      error: 'Forbidden', message: 'Permission denied: users:write', permission: 'users:write',
    }), { status: 403, headers: { 'Content-Type': 'application/json' } })));
    await expect(gatewayApiFetch('http://127.0.0.1:4000/api')).rejects.toMatchObject({
      envelope: {
        code: 'KB_AUTH_FORBIDDEN',
        details: { permission: 'users:write' },
        message: 'You do not have permission to perform this operation.',
      },
    });
  });
});
