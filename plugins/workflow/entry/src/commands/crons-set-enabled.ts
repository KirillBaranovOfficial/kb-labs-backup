import { defineCommand, handleError, type PluginContextV3, type CLIInput, type CommandResult } from '@kb-labs/sdk';
import { WorkflowDaemonClient } from '../http-client.js';
import { workflowCommandError } from './errors.js';

export interface CronEnabledFlags { json?: boolean }

export function createCronEnabledCommand(enabled: boolean) {
  const action = enabled ? 'enable' : 'disable';
  return defineCommand<unknown, CLIInput<CronEnabledFlags>, unknown>({
    id: `workflow:crons-${action}`,
    description: `${enabled ? 'Enable' : 'Disable'} a project workflow cron`,
    handler: {
      async execute(ctx: PluginContextV3, input): Promise<CommandResult> {
        const id = input.argv?.[0];
        const outputJson = input.flags?.json ?? false;
        if (!id) {
          workflowCommandError(ctx, 'KB_RUNTIME_INPUT_INVALID', 'Missing cron ID.', `Run kb workflow crons ${action} --help to see the required arguments.`, outputJson);
          return { ok: false, error: 'Command failed' };
        }
        try {
          const result = await new WorkflowDaemonClient().setCronEnabled(id, enabled);
          if (outputJson) { ctx.ui?.json?.({ ok: true, data: { id, ...result } }); }
          else { ctx.ui?.success?.(`Cron ${id} ${enabled ? 'enabled' : 'disabled'}`); }
          return { ok: true };
        } catch (error) {
          const message = error instanceof Error ? error.message : String(error);
          if (message.startsWith('CRON_PLUGIN_READ_ONLY:')) {
            workflowCommandError(ctx, 'KB_RUNTIME_COMMAND_FAILED', 'Plugin cron schedules are read-only.', 'List available schedules with: kb workflow crons list', outputJson, message);
            return { ok: false, error: 'Command failed' };
          }
          handleError(ctx, error, outputJson);
          if (!outputJson) { ctx.ui?.warn?.(`Run: kb workflow crons ${action} ${id}`); }
          return { ok: false, error: 'Command failed' };
        }
      },
    },
  });
}
