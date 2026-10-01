/**
 * workflow job logs command - Get raw job logs
 */

import { defineCommand, validationError, handleError, type PluginContextV3 , type CommandResult} from '@kb-labs/sdk';
import { WorkflowDaemonClient } from '../http-client.js';

type LogsInput = { argv?: string[]; json?: boolean; flags?: { json?: boolean } };

type LogEntry = { level: string; message: string; timestamp: string; stepId?: string; [key: string]: unknown };

function renderLogs(ctx: PluginContextV3, logs: LogEntry[], outputJson: boolean): void {
  if (outputJson) {
    ctx.ui?.json?.({ ok: true, data: { logs } });
    return;
  }

  if (logs.length === 0) {
    ctx.ui?.warn?.('No logs found');
    return;
  }

  for (const log of logs) {
    const level = (log.level ?? 'info') as 'info' | 'warn' | 'error' | 'debug';
    const prefix = log.stepId ? `[${log.stepId}] ` : '';
    ctx.ui?.log?.({ level, message: `${prefix}${log.message}` });
  }
}

export default defineCommand<unknown, LogsInput, unknown>({
  id: 'workflow:job-logs',
  description: 'Get logs for a raw workflow job',

  handler: {
    async execute(ctx: PluginContextV3, input: LogsInput): Promise<CommandResult> {
      const flags = input.flags ?? input;
      const outputJson = flags.json ?? false;
      const jobId = input.argv?.[0];

      if (!jobId) {
        validationError(ctx, 'Missing job ID', 'Usage: kb workflow job logs <jobId>', outputJson);
        return { ok: false, error: 'Command failed' };
      }

      try {
        const client = new WorkflowDaemonClient();

        const logs = await client.getJobLogs(jobId);
        renderLogs(ctx, logs as LogEntry[], outputJson);

        return { ok: true };
      } catch (error) {
        handleError(ctx, error, outputJson);
        return { ok: false, error: 'Command failed' };
      }
    },
  },
});
