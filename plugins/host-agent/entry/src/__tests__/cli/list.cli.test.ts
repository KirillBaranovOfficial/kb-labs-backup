import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
const gatewayApiFetch = vi.hoisted(() => vi.fn());
vi.mock('@kb-labs/cli-runtime/gateway', () => ({
  gatewayApiFetch,
  GatewayApiError: class GatewayApiError extends Error {
    envelope = { code: 'KB_AUTH_REQUIRED', message: 'Authentication is required.', hint: 'Run "kb auth login".' };
  },
}));
import { mockCLIInput, createCapturedUI, createMockContext } from '@kb-labs/shared-testing-e2e/cli';

// Mock fs to prevent reading ~/.kb/agent.json during tests
vi.mock('node:fs', () => ({
  readFileSync: vi.fn().mockImplementation(() => {
    throw new Error('ENOENT');
  }),
}));

import listCommand from '../../commands/list.js';

beforeEach(() => {
  vi.resetAllMocks();
});

afterEach(() => {
  gatewayApiFetch.mockReset();
});

const makeHost = (overrides: Partial<{
  hostId: string;
  name: string;
  status: string;
  capabilities: string[];
  lastSeen: number;
  connections: string[];
}> = {}) => ({
  hostId: 'host-1',
  name: 'my-machine',
  status: 'online',
  capabilities: ['filesystem', 'git'],
  lastSeen: Date.now() - 5000,
  connections: [],
  ...overrides,
});

describe('workspace:list', () => {
  it('CL-01: returns exitCode 0 and prints success when hosts are returned', async () => {
    gatewayApiFetch.mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ hosts: [makeHost()] }),
    } as Response);

    const { ui, captured } = createCapturedUI();
    const ctx = createMockContext({ ui });
    const result = await listCommand.execute(ctx, mockCLIInput({ flags: { gateway: 'http://localhost:4000' } }));

    expect(result.ok).toBe(true);
    expect(result).toMatchObject({ result: { hosts: [expect.any(Object)] } });
    expect(captured.success.length).toBeGreaterThan(0);
  });

  it('CL-02: --json flag outputs hosts array and returns exitCode 0', async () => {
    const host = makeHost();
    gatewayApiFetch.mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ hosts: [host] }),
    } as Response);

    const { ui, captured } = createCapturedUI();
    const ctx = createMockContext({ ui });
    const result = await listCommand.execute(
      ctx,
      mockCLIInput({ flags: { gateway: 'http://localhost:4000', json: true } }),
    );

    expect(result.ok).toBe(true);
    expect(result.result).toMatchObject({ hosts: [{ hostId: 'host-1' }] });
  });

  it('CL-03: empty host list prints info message and returns exitCode 0', async () => {
    gatewayApiFetch.mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ hosts: [] }),
    } as Response);

    const { ui, captured } = createCapturedUI();
    const ctx = createMockContext({ ui });
    const result = await listCommand.execute(
      ctx,
      mockCLIInput({ flags: { gateway: 'http://localhost:4000' } }),
    );

    expect(result.ok).toBe(true);
    expect(result).toMatchObject({ result: { hosts: [] } });
    expect(captured.infos.length).toBeGreaterThan(0);
  });

  it('CL-04: gateway returns HTTP 401 — exitCode 1, error reported', async () => {
    gatewayApiFetch.mockResolvedValue({
      ok: false,
      status: 401,
      json: async () => ({}),
    } as Response);

    const { ui, captured } = createCapturedUI();
    const ctx = createMockContext({ ui });
    const result = await listCommand.execute(
      ctx,
      mockCLIInput({ flags: { gateway: 'http://localhost:4000' } }),
    );

    expect(result.ok).toBe(false);
    expect(result).toMatchObject({ ok: false, error: { code: expect.stringMatching(/^KB_/), message: expect.any(String), hint: expect.any(String) } });
  });

  it('CL-05: fetch throws (gateway unreachable) — exitCode 1, error reported', async () => {
    gatewayApiFetch.mockRejectedValue(new Error('ECONNREFUSED'));

    const { ui, captured } = createCapturedUI();
    const ctx = createMockContext({ ui });
    const result = await listCommand.execute(
      ctx,
      mockCLIInput({ flags: { gateway: 'http://localhost:4000' } }),
    );

    expect(result.ok).toBe(false);
    expect(result).toMatchObject({ ok: false, error: { code: expect.stringMatching(/^KB_/), message: expect.any(String), hint: expect.any(String) } });
  });

  it('CL-06: multiple hosts — all returned in result', async () => {
    const hosts = [
      makeHost({ hostId: 'h1', name: 'machine-1', status: 'online' }),
      makeHost({ hostId: 'h2', name: 'machine-2', status: 'offline' }),
    ];
    gatewayApiFetch.mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ hosts }),
    } as Response);

    const { ui } = createCapturedUI();
    const ctx = createMockContext({ ui });
    const result = await listCommand.execute(
      ctx,
      mockCLIInput({ flags: { gateway: 'http://localhost:4000' } }),
    );

    expect(result.ok).toBe(true);
    expect(result).toMatchObject({ result: { hosts: [expect.any(Object), expect.any(Object)] } });
  });
});
