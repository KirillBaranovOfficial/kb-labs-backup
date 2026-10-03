import { describe, expect, it, vi, beforeEach } from 'vitest';
import { mockCLIInput, createCapturedUI, createMockContext } from '@kb-labs/shared-testing-e2e/cli';
import type { CronListResponse } from '@kb-labs/workflow-contracts';
import { makeClient } from '../helpers/defaults.js';
import { WorkflowDaemonClient } from '../../http-client.js';
import cronListCommand from '../../commands/crons-list.js';
import enableCommand from '../../commands/crons-enable.js';
import { manifest } from '../../manifest.js';

vi.mock('../../http-client.js', () => ({ WorkflowDaemonClient: vi.fn(function WorkflowDaemonClientMock() {}) }));
const MockedClient = vi.mocked(WorkflowDaemonClient);
beforeEach(() => MockedClient.mockReset());

describe('workflow crons CLI', () => {
  it('routes cron mutations to default-exported command handlers', () => {
    const commands = manifest.cli.commands;
    expect(commands.find((command) => command.path === 'workflow crons enable')?.handler)
      .toBe('./commands/crons-enable.js#default');
    expect(commands.find((command) => command.path === 'workflow crons disable')?.handler)
      .toBe('./commands/crons-disable.js#default');
  });

  const response: CronListResponse = {
    crons: [
      { id: 'nightly', source: 'user', schedule: '0 2 * * *', target: { workflow: 'release' }, enabled: true, nextFireAt: '2026-10-02T02:00:00.000Z' },
      { id: 'plugin:qa:cleanup', source: 'plugin', schedule: '0 3 * * *', target: { handler: 'qa:cleanup' }, enabled: true },
    ],
    errors: [{ file: '.kb/crons/broken.yml', reason: 'Invalid cron expression' }],
  };

  it('lists both sources and reports invalid project files as JSON diagnostics', async () => {
    MockedClient.mockImplementation(function MockedClientImplementation() {
      return makeClient({ getCronJobs: async () => response });
    });
    const { ui, captured } = createCapturedUI();
    const result = await cronListCommand.execute(createMockContext({ ui }), mockCLIInput({ flags: { json: true } }));
    expect(result.ok).toBe(true);
    expect(captured.json[0]).toMatchObject({ ok: true, data: response });
  });

  it('gives a typed next action when a plugin cron cannot be enabled', async () => {
    MockedClient.mockImplementation(function MockedClientImplementation() {
      return makeClient({ setCronEnabled: async () => { throw new Error('CRON_PLUGIN_READ_ONLY: Plugin cron plugin:qa:cleanup is read-only'); } });
    });
    const { ui, captured } = createCapturedUI();
    const result = await enableCommand.execute(createMockContext({ ui }), mockCLIInput({ argv: ['plugin:qa:cleanup'], flags: { json: true } }));
    expect(result.ok).toBe(false);
    expect(captured.json[0]).toMatchObject({ ok: false, error: { code: 'KB_RUNTIME_COMMAND_FAILED', hint: expect.any(String) } });
  });
});
