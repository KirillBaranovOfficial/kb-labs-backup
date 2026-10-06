import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mockCLIInput, createCapturedUI, createMockContext } from '@kb-labs/shared-testing-e2e/cli';
import { makeClient } from '../helpers/defaults.js';

vi.mock('../../http-client.js', () => ({
  WorkflowDaemonClient: vi.fn(function WorkflowDaemonClientMock() {}),
}));

import { WorkflowDaemonClient } from '../../http-client.js';
import logsCommand from '../../commands/logs.js';

const MockedClient = vi.mocked(WorkflowDaemonClient);

const sampleLogs = [
  { level: 'info' as const, message: 'Step started', timestamp: new Date().toISOString() },
  { level: 'warn' as const, message: 'Retrying step', timestamp: new Date().toISOString() },
  { level: 'info' as const, message: 'Step finished', timestamp: new Date().toISOString(), stepId: 'step-1' },
];

beforeEach(() => {
  MockedClient.mockReset();
});

describe('workflow job logs', () => {
  // case: S-185.4
  it('CLG-01: positional job ID outputs log lines via job endpoint', async () => {
    MockedClient.mockImplementation(function MockedClientImplementation() {
      return makeClient({
        getJobLogs: async () => sampleLogs,
      });
    });

    const { ui, captured } = createCapturedUI();
    const ctx = createMockContext({ ui });
    const result = await logsCommand.execute(ctx, mockCLIInput({ argv: ['j-abc'] }));

    expect(result.ok).toBe(true);
    expect(captured.logs.length).toBe(3);
  });

  it('CLG-02: --json returns { ok: true, data: { logs } }', async () => {
    MockedClient.mockImplementation(function MockedClientImplementation() {
      return makeClient({
        getJobLogs: async () => sampleLogs,
      });
    });

    const { ui, captured } = createCapturedUI();
    const ctx = createMockContext({ ui });
    const result = await logsCommand.execute(ctx, mockCLIInput({ argv: ['j-abc'], flags: { json: true } }));

    expect(result.ok).toBe(true);
    const payload = result as { ok: boolean; result: { logs: typeof sampleLogs } };
    expect(payload.ok).toBe(true);
    expect(Array.isArray(payload.result.logs)).toBe(true);
    expect(payload.result.logs.length).toBe(3);
  });

  it('CLG-03: missing positional job ID returns exitCode 1', async () => {
    const { ui, captured } = createCapturedUI();
    const ctx = createMockContext({ ui });
    const result = await logsCommand.execute(ctx, mockCLIInput({ flags: {} }));

    expect(result.ok).toBe(false);
    if (!result.ok) { expect(result.error.hint).toBeTruthy(); }
  });

  it('CLG-04: empty logs renders empty state without throwing', async () => {
    MockedClient.mockImplementation(function MockedClientImplementation() {
      return makeClient({
        getJobLogs: async () => [],
      });
    });

    const { ui, captured } = createCapturedUI();
    const ctx = createMockContext({ ui });
    const result = await logsCommand.execute(ctx, mockCLIInput({ argv: ['j-empty'] }));

    expect(result.ok).toBe(true);
    expect(captured.errors.length).toBe(0);
    expect(captured.warnings.length).toBeGreaterThan(0);
  });

  it('CLG-05: daemon unavailable returns exitCode 1', async () => {
    MockedClient.mockImplementation(function MockedClientImplementation() {
      return makeClient({
        getJobLogs: async () => { throw new Error('ECONNREFUSED'); },
      });
    });

    const { ui, captured } = createCapturedUI();
    const ctx = createMockContext({ ui });
    const result = await logsCommand.execute(ctx, mockCLIInput({ argv: ['j-123'] }));

    expect(result.ok).toBe(false);
    if (!result.ok) { expect(result.error.hint).toBeTruthy(); }
  });

});
