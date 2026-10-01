import { beforeEach, describe, expect, it, vi } from 'vitest';
import { noopUI, noopTraceContext } from '@kb-labs/plugin-contracts';
import type { PluginContextV3 } from '@kb-labs/plugin-contracts';

const sessionLoad = vi.fn();
vi.mock('@kb-labs/cli-runtime/gateway', () => ({
  SessionManager: class {
    load = sessionLoad;
    isExpired = () => false;
    refresh = vi.fn();
  },
}));

import { authTokens } from '../commands/system/auth/auth-tokens.js';

function makeContext(output: string[], errors: string[], json: unknown[]): PluginContextV3 {
  return {
    host: 'cli', requestId: 'test', pluginId: '@kb-labs/system', pluginVersion: '1.0.0', cwd: process.cwd(),
    ui: { ...noopUI, write: (value: string) => output.push(value), error: (value: string) => errors.push(value), json: (value: unknown) => json.push(value) },
    platform: {}, runtime: { fs: {}, fetch: vi.fn(), env: vi.fn() }, api: {},
    hostContext: { host: 'cli', argv: [], flags: {} }, trace: noopTraceContext,
  } as unknown as PluginContextV3;
}

beforeEach(() => {
  vi.clearAllMocks();
  sessionLoad.mockResolvedValue({ gatewayUrl: 'https://gateway.test', accessToken: 'access', refreshToken: 'refresh', expiresAt: Date.now() + 60_000 });
});

describe('kb auth tokens', () => {
  it('lists tokens for the signed-in owner', async () => {
    const output: string[] = [];
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({ tokens: [{ tokenId: 'tok-1', name: 'CI', issuedAt: 1, expiresAt: 2 }] }) }));
    const code = await authTokens.run(makeContext(output, [], []), [], {});
    expect(code).toBe(0);
    expect(fetch).toHaveBeenCalledWith('https://gateway.test/auth/tokens', expect.objectContaining({ headers: { Authorization: 'Bearer access' } }));
    expect(output.join('')).toContain('tok-1');
  });

  it('revokes an owner token by ID', async () => {
    const output: string[] = [];
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({ ok: true }) }));
    const code = await authTokens.run(makeContext(output, [], []), [], { revoke: 'tok-1' });
    expect(code).toBe(0);
    expect(fetch).toHaveBeenCalledWith('https://gateway.test/auth/tokens/tok-1/revoke', expect.objectContaining({ method: 'POST' }));
    expect(output.join('')).toContain('Revoked');
  });

  it('creates a token with an optional expiry expressed in days', async () => {
    const output: string[] = [];
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true, status: 201,
      json: async () => ({ token: 'kbl_pat_secret', tokenRecord: { tokenId: 'tok-2', name: 'nightly CI', issuedAt: 1, expiresAt: 2 } }),
    });
    vi.stubGlobal('fetch', fetchMock);
    const json: unknown[] = [];
    const code = await authTokens.run(makeContext(output, [], json), [], {
      name: 'nightly CI', 'expires-in-days': '90', json: true,
    });
    expect(code).toBe(0);
    expect(JSON.parse(fetchMock.mock.calls[0]?.[1]?.body as string))
      .toEqual({ name: 'nightly CI', expiresInDays: 90 });
    expect(json[0]).toMatchObject({ ok: true, token: 'kbl_pat_secret' });
  });

  it('rejects an invalid expiry with the exact retry command', async () => {
    const errors: string[] = [];
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    const code = await authTokens.run(makeContext([], errors, []), [], {
      name: 'nightly CI', 'expires-in-days': '0',
    });
    expect(code).toBe(1);
    expect(errors[0]).toContain('kb auth tokens --name <name> --expires-in-days 90');
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
