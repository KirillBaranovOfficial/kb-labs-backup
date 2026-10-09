/**
 * workflow:runs-logs <runId> command — fetch logs for a workflow run
 */

import { defineCommand, validationError, handleError, type CLIInput, type PluginCommandContextV3 , type CommandResult} from '@kb-labs/sdk';
import { WorkflowDaemonClient } from '../http-client.js';
import type { RunsLogsFlags } from '../flags.js';

type LogEntry = { level: string; message: string; timestamp: string; stepId?: string; stepName?: string; [key: string]: unknown };

function renderLogs(ctx: PluginCommandContextV3, logs: LogEntry[], outputJson: boolean): void {
  if (outputJson) {
    return;
  }

  if (logs.length === 0) {
    ctx.ui?.warn?.('No logs found');
    return;
  }

  for (const log of logs) {
    const level = (log.level ?? 'info') as 'info' | 'warn' | 'error' | 'debug';
    const label = log.stepName ?? log.stepId;
    const prefix = label ? `[${label}] ` : '';
    ctx.ui?.log?.({ level, message: `${prefix}${log.message}` });
  }
}

export default defineCommand<unknown, CLIInput<RunsLogsFlags>, unknown>({
  id: 'workflow:runs-logs',
  description: 'Fetch logs for a workflow run',

  handler: {
    async execute(ctx: PluginCommandContextV3, input: CLIInput<RunsLogsFlags>): Promise<CommandResult> {
      const { flags, argv = [] } = input;
      const outputJson = flags?.json ?? false;
      const runId = flags?.['run-id'] ?? argv[0];

      if (!runId) {
        return validationError('Missing run ID', 'Usage: kb workflow runs logs <runId>');
      }

      try {
        const client = new WorkflowDaemonClient();
        const logs = await client.getRunLogs(runId, {
          stepId: flags?.step,
          failedOnly: flags?.['log-failed'],
        });
        renderLogs(ctx, logs as LogEntry[], outputJson);
        return { logs };
      } catch (error) {
        return handleError(error);
      }
    },
  },
});
