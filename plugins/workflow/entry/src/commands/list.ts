/**
 * workflow:list command - List active executions
 */

import { defineCommand, handleError, type PluginContextV3 , type CommandResult} from '@kb-labs/sdk';
import { type ListFlags } from '@kb-labs/workflow-contracts';
import { WorkflowDaemonClient } from '../http-client.js';

type ListInput = ListFlags & { argv?: string[]; flags?: ListFlags };

export default defineCommand<unknown, ListInput, unknown>({
  id: 'workflow:list',
  description: 'List active workflow executions',

  handler: {
    // eslint-disable-next-line sonarjs/cognitive-complexity -- Workflow listing with filtering (status/type), JSON/human output formats, run state formatting, and error handling
    async execute(ctx: PluginContextV3, input: ListInput): Promise<CommandResult> {
      const flags = input.flags ?? input;
      const outputJson = flags.json ?? false;
      const statusFilter = flags.status;
      ctx.ui?.warn?.('workflow list is deprecated; use: kb workflow runs list');

      try {
        const client = new WorkflowDaemonClient();

        // Default: list active executions (runs)
        let executions = await client.getExecutions();

        // Filter by status if provided
        if (statusFilter) {
          executions = executions.filter((exec) => exec.status === statusFilter);
        }

        if (outputJson) {
          ctx.ui?.json?.({ ok: true, data: { executions } });
        } else {
          if (executions.length === 0) {
            ctx.ui?.warn?.('No active executions found');
          } else {
            ctx.ui?.table?.(
              executions.map(exec => ({
                id: exec.id,
                status: exec.status,
                started: exec.startedAt || 'N/A',
              })),
              [
                { header: 'ID', key: 'id' },
                { header: 'Status', key: 'status' },
                { header: 'Started', key: 'started' },
              ],
            );
            const filterNote = statusFilter ? ` (filter: ${statusFilter})` : '';
            ctx.ui?.success?.(`${executions.length} execution${executions.length === 1 ? '' : 's'}${filterNote}`);
          }
        }

        return { ok: true };
      } catch (error) {
        handleError(ctx, error, outputJson);
        if (!outputJson) { ctx.ui?.warn?.('Start with: kb-dev start workflow'); }
        return { ok: false, error: 'Command failed' };
      }
    },
  },
});
