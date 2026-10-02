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

import { authResetLink } from '../commands/system/auth/auth-reset-link.js';

function context(errors: string[], json: unknown[]): PluginContextV3 {
  return {
    host: 'cli', requestId: 'test', pluginId: '@kb-labs/system', pluginVersion: '1.0.0', cwd: process.cwd(),
    ui: { ...noopUI, error: (value: string) => errors.push(value), json: (value: unknown) => json.push(value) },
    platform: {}, runtime: { fs: {}, fetch: vi.fn(), env: vi.fn() }, api: {},
    hostContext: { host: 'cli', argv: [], flags: {} }, trace: noopTraceContext,
  } as unknown as PluginContextV3;
}

beforeEach(() => gatewayApiFetch.mockReset());

describe('kb auth reset-link', () => {
  it('calls the shared gateway client for the password reset link', async () => {
    const errors: string[] = [];
    const json: unknown[] = [];
    gatewayApiFetch.mockResolvedValue(new Response(JSON.stringify({ ok: true, data: { resetUrl: 'https://gateway.test/reset', expiresAt: 1 } }), { status: 200 }));
    const result = await authResetLink.run(context(errors, json), [], { 'user-id': 'user-1', json: true });
    expect(gatewayApiFetch).toHaveBeenCalledWith('http://127.0.0.1:4000/auth/users/user-1/password-reset-link', { method: 'POST' });
    expect(result).toBe(0);
    expect(json[0]).toMatchObject({ ok: true, resetUrl: 'https://gateway.test/reset' });
  });
});
