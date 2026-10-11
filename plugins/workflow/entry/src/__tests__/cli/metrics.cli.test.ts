import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mockCLIInput, createCapturedUI, createMockContext } from '@kb-labs/shared-testing-e2e/cli';
import { makeClient } from '../helpers/defaults.js';

vi.mock('../../http-client.js', () => ({
  WorkflowDaemonClient: vi.fn(function WorkflowDaemonClientMock() {}),
}));

import { WorkflowDaemonClient } from '../../http-client.js';
import type { WorkflowMetricsData } from '../../http-client.js';
import metricsCommand from '../../commands/metrics.js';

const MockedClient = vi.mocked(WorkflowDaemonClient);

const sampleMetrics: WorkflowMetricsData = {
  runs: { total: 42, queued: 2, running: 3, success: 35, failed: 2, cancelled: 0 },
  jobs: { total: 120, queued: 5, running: 8, success: 100, failed: 7 },
};

beforeEach(() => {
  MockedClient.mockReset();
});

describe('workflow:metrics', () => {
  it('CM-01: renders table with run and job counts', async () => {
    MockedClient.mockImplementation(function MockedClientImplementation() {
      return makeClient({
        getMetrics: async () => sampleMetrics,
      });
    });

    const { ui, captured } = createCapturedUI();
    const ctx = createMockContext({ ui });
    const result = await metricsCommand.execute(ctx, mockCLIInput({ flags: {} }));

    expect(captured.success.length).toBeGreaterThan(0);
    expect(captured.success[0]?.message).toContain('Metrics');
  });

  it('CM-02: --json returns { ok: true, data: WorkflowMetricsData }', async () => {
    MockedClient.mockImplementation(function MockedClientImplementation() {
      return makeClient({
        getMetrics: async () => sampleMetrics,
      });
    });

    const { ui, captured } = createCapturedUI();
    const ctx = createMockContext({ ui });
    const result = await metricsCommand.execute(ctx, mockCLIInput({ flags: { json: true } }));

    const payload = result as WorkflowMetricsData;
    expect(payload.runs.total).toBe(42);
    expect(payload.jobs.total).toBe(120);
  });

  it('CM-03: daemon unavailable returns exitCode 1', async () => {
    MockedClient.mockImplementation(function MockedClientImplementation() {
      return makeClient({
        getMetrics: async () => { throw new Error('ECONNREFUSED'); },
      });
    });

    const { ui, captured } = createCapturedUI();
    const ctx = createMockContext({ ui });
    const result = await metricsCommand.execute(ctx, mockCLIInput({ flags: {} }));

    expect(result).toHaveProperty('ok', false);
    expect(result).toMatchObject({ ok: false, error: { hint: expect.any(String) } });
  });

  // case: S-203.1
  it('keeps daemon failures identical in human and JSON modes', async () => {
    MockedClient.mockImplementation(function MockedClientImplementation() {
      return makeClient({ getMetrics: async () => { throw new Error('ECONNREFUSED'); } });
    });
    const { ui } = createCapturedUI();
    const ctx = createMockContext({ ui });
    const humanResult = await metricsCommand.execute(ctx, mockCLIInput({ flags: {} }));
    const jsonResult = await metricsCommand.execute(ctx, mockCLIInput({ flags: { json: true } }));

    expect(humanResult).toHaveProperty('ok', false);
    expect(jsonResult).toEqual(humanResult);
  });
});
