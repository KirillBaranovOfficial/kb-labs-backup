import { beforeEach, describe, expect, it, vi } from 'vitest';
import { noopUI, noopTraceContext } from '@kb-labs/plugin-contracts';
import type { PluginContextV3 } from '@kb-labs/plugin-contracts';

const gatewayApiFetch = vi.hoisted(() => vi.fn());
vi.mock('@kb-labs/cli-runtime/gateway', () => ({
  gatewayApiFetch,
  readGatewaySuccessData: async (response: Response) => (await response.json() as { data: unknown }).data,
  GatewayApiError: class GatewayApiError extends Error {
    constructor(readonly envelope: Record<string, unknown>) { super(String(envelope.message)); }
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

beforeEach(() => gatewayApiFetch.mockReset());

describe('kb auth tokens', () => {
  it('lists tokens through the shared gateway client', async () => {
    const output: string[] = [];
    gatewayApiFetch.mockResolvedValue(new Response(JSON.stringify({ ok: true, data: { tokens: [{ tokenId: 'tok-1', name: 'CI', issuedAt: 1, expiresAt: 2 }] } }), { status: 200 }));
    const code = await authTokens.run(makeContext(output, [], []), [], {});
    expect(code).toBe(0);
    expect(gatewayApiFetch).toHaveBeenCalledWith('http://127.0.0.1:4000/auth/tokens', expect.objectContaining({ method: 'GET' }));
    expect(output.join('')).toContain('tok-1');
  });

});
