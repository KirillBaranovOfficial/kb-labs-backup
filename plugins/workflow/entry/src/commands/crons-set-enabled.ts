import { defineCommand, handleError, type PluginContextV3, type CLIInput, type CommandResult } from '@kb-labs/sdk';
import { WorkflowDaemonClient } from '../http-client.js';

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
          const message = `Missing cron id. Usage: kb workflow crons ${action} <id>`;
          if (outputJson) { ctx.ui?.json?.({ ok: false, error: message }); }
          else { ctx.ui?.error?.(message); }
          return { ok: false, error: message };
        }
        try {
          const result = await new WorkflowDaemonClient().setCronEnabled(id, enabled);
          if (outputJson) { ctx.ui?.json?.({ ok: true, data: { id, ...result } }); }
          else { ctx.ui?.success?.(`Cron ${id} ${enabled ? 'enabled' : 'disabled'}`); }
          return { ok: true };
        } catch (error) {
          const message = error instanceof Error ? error.message : String(error);
          if (message.startsWith('CRON_PLUGIN_READ_ONLY:')) {
            const text = `${message}. Plugin crons are read-only; list schedules with: kb workflow crons list`;
            if (outputJson) { ctx.ui?.json?.({ ok: false, error: text, code: 'CRON_PLUGIN_READ_ONLY' }); }
            else { ctx.ui?.error?.(text); }
            return { ok: false, error: text };
          }
          handleError(ctx, error, outputJson);
          if (!outputJson) { ctx.ui?.warn?.(`Run: kb workflow crons ${action} ${id}`); }
          return { ok: false, error: 'Command failed' };
        }
      },
    },
  });
}
