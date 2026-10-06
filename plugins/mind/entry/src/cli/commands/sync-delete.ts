import { defineCommand, handleError, confirmDestructive, type CLIInput, type PluginCommandContextV3 } from '@kb-labs/sdk';
import type { CommandResult } from '@kb-labs/sdk';
import { type SyncDeleteFlags, type SyncResponse } from '@kb-labs/mind-contracts';
import { buildMind } from '../../platform';

export default defineCommand<unknown, CLIInput<SyncDeleteFlags>, SyncResponse>({
  id: 'mind:sync-delete',
  description: 'Remove documents from an index (incremental)',

  handler: {
    async execute(ctx: PluginCommandContextV3, input: CLIInput<SyncDeleteFlags>): Promise<CommandResult<SyncResponse>> {
      try {
      const paths = input.argv ?? [];
      if (paths.length === 0) {

        return { ok: false, error: { code: 'KB_RUNTIME_INPUT_INVALID', message: 'Provide one or more paths to delete.', hint: 'Run `kb mind sync delete <paths...>` with the paths to remove.' } };
      }
      // Destructive: blocks in EVERY mode until --yes (agents get a structured
      // confirmationRequired signal with severity/recovery, not a silent delete).
      const blocked = confirmDestructive(ctx, {
        confirmed: input.flags.yes,
        isJson: input.flags.json,
        action: {
          action: 'mind sync delete',
          resource: `index "${input.flags.index ?? 'default'}"`,
          effect: 'removes the specified document(s)',
          severity: 'medium',
          reversible: true,
          recovery: 're-add with `kb mind sync add <paths>`',
          blastRadius: { count: paths.length, unit: 'document(s)' },
        },
      });
      if (blocked) {
        return {
          ok: false,
          error: {
            code: 'KB_RUNTIME_CONFIRMATION_REQUIRED',
            message: 'Removing indexed documents requires explicit confirmation.',
            hint: `Verify the ${paths.length} document path(s), then rerun: kb mind sync delete ${paths.join(' ')} --yes`,
          },
          result: blocked.result,
        };
      }
      const mind = await buildMind(ctx.cwd);
      const res = await mind.syncDelete(paths, input.flags.index);
      if (input.flags.json) {
        return { ok: true, result: res };
        return { ok: true, result: res };
      }
      ctx.ui?.success?.(`Sync delete → "${res.indexId}": ${res.deleted} chunk(s) removed`);
      return { ok: true, result: res };
      } catch (err) {
        return handleError(ctx, err, input.flags.json);
      }
    },
  },
});
