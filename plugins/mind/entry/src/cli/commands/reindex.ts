import { defineCommand, handleError, type CLIInput, type PluginCommandContextV3 } from '@kb-labs/sdk';
import type { CommandResult } from '@kb-labs/sdk';
import { type ReindexFlags, type IndexResponse } from '@kb-labs/mind-contracts';
import { buildMind } from '../../platform';

export default defineCommand<unknown, CLIInput<ReindexFlags>, IndexResponse>({
  id: 'mind:reindex',
  description: 'Rebuild an index from source files',

  handler: {
    async execute(ctx: PluginCommandContextV3, input: CLIInput<ReindexFlags>): Promise<CommandResult<IndexResponse>> {
      try {
      const mind = await buildMind(ctx.cwd);
      const res = await mind.reindex({ indexId: input.flags.index, full: input.flags.full });
      if (input.flags.json) {
        return { ok: true, result: res };
        return { ok: true, result: res };
      }
      ctx.ui?.success?.(
        `Reindexed "${res.indexId}": ${res.filesIndexed} file(s), ${res.chunks} chunk(s) (${res.durationMs}ms)`,
      );
      return { ok: true, result: res };
      } catch (err) {
        return handleError(ctx, err, input.flags.json);
      }
    },
  },
});
