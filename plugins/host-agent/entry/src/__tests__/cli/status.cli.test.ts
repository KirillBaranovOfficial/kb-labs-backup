import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mockCLIInput, createCapturedUI, createMockContext } from '@kb-labs/shared-testing-e2e/cli';
const clientMocks = vi.hoisted(() => ({
  connect: vi.fn<() => Promise<void>>(),
  status: vi.fn<() => Promise<{ connected: boolean; hostId: string; gatewayUrl: string }>>(),
  close: vi.fn(),
}));

vi.mock('@kb-labs/host-agent-transport', () => ({
  createTransport: vi.fn().mockReturnValue({}),
}));

vi.mock('@kb-labs/host-agent-client', () => ({
  HostAgentClient: class HostAgentClientMock {
    connect() { return clientMocks.connect(); }
    status() { return clientMocks.status(); }
    close() { clientMocks.close(); }
  },
}));

import statusCommand from '../../commands/status.js';

beforeEach(() => {
  vi.resetAllMocks();
  vi.stubGlobal('fetch', vi.fn());
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('workspace:status', () => {
  it('CS-01: daemon not running — exitCode 0, running: false', async () => {
    // Simulate IPC unreachable: connect throws
    clientMocks.connect.mockRejectedValue(new Error('ENOENT'));
    clientMocks.status.mockRejectedValue(new Error('not connected'));

    const { ui, captured } = createCapturedUI();
    const ctx = createMockContext({ ui });
    const result = await statusCommand.execute(ctx, mockCLIInput({ flags: {} }));

    expect(result.ok).toBe(true);
    expect(result.result.running).toBe(false);
    expect(captured.infos.length).toBeGreaterThan(0);
  });

  it('CS-02: daemon running and connected — exitCode 0, running: true, connected: true', async () => {
    clientMocks.connect.mockResolvedValue(undefined);
    clientMocks.status.mockResolvedValue({ connected: true, hostId: 'host-123', gatewayUrl: 'http://localhost:4000' });

    const { ui, captured } = createCapturedUI();
    const ctx = createMockContext({ ui });
    const result = await statusCommand.execute(ctx, mockCLIInput({ flags: {} }));

    expect(result.ok).toBe(true);
    expect(result.result.running).toBe(true);
    expect(result.result.connected).toBe(true);
    expect(result.result.hostId).toBe('host-123');
    expect(captured.success.length).toBeGreaterThan(0);
  });

  it('CS-03: daemon running but disconnected — exitCode 0, running: true, connected: false', async () => {
    clientMocks.connect.mockResolvedValue(undefined);
    clientMocks.status.mockResolvedValue({ connected: false, hostId: 'host-456', gatewayUrl: 'http://localhost:4000' });

    const { ui, captured } = createCapturedUI();
    const ctx = createMockContext({ ui });
    const result = await statusCommand.execute(ctx, mockCLIInput({ flags: {} }));

    expect(result.ok).toBe(true);
    expect(result.result.running).toBe(true);
    expect(result.result.connected).toBe(false);
    expect(captured.success.length).toBeGreaterThan(0);
  });

  it('CS-04: --json flag with daemon not running outputs { running: false }', async () => {
    clientMocks.connect.mockRejectedValue(new Error('ENOENT'));
    clientMocks.status.mockRejectedValue(new Error('not connected'));

    const { ui, captured } = createCapturedUI();
    const ctx = createMockContext({ ui });
    const result = await statusCommand.execute(ctx, mockCLIInput({ flags: { json: true } }));

    expect(result.ok).toBe(true);
    expect(result.result.running).toBe(false);
    expect(captured.json[0]).toMatchObject({ running: false });
  });

  it('CS-05: --json flag with connected daemon outputs full status JSON', async () => {
    clientMocks.connect.mockResolvedValue(undefined);
    clientMocks.status.mockResolvedValue({ connected: true, hostId: 'host-789', gatewayUrl: 'http://gw:4000' });

    const { ui, captured } = createCapturedUI();
    const ctx = createMockContext({ ui });
    const result = await statusCommand.execute(ctx, mockCLIInput({ flags: { json: true } }));

    expect(result.ok).toBe(true);
    expect(captured.json[0]).toMatchObject({
      running: true,
      connected: true,
      hostId: 'host-789',
    });
  });
});
