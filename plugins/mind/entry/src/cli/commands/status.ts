import { defineCommand, handleError, type CLIInput, type PluginCommandContextV3 } from '@kb-labs/sdk';
import type { CommandResult } from '@kb-labs/sdk';
import { type StatusFlags, type IndexSummary, type StatusResponse } from '@kb-labs/mind-contracts';
import { buildMind } from '../../platform';

export default defineCommand<unknown, CLIInput<StatusFlags>, StatusResponse>({
  id: 'mind:status',
  description: 'Show Mind index status',

  handler: {
    async execute(ctx: PluginCommandContextV3, input: CLIInput<StatusFlags>): Promise<CommandResult<StatusResponse>> {
      const { flags } = input;
      try {
        const mind = await buildMind(ctx.cwd);
        const res = await mind.status(flags.index);

        if (flags.json) {
          return { ok: true, result: res };
          return { ok: true, result: res };
        }

        if (res.indexes.length === 0) {
          ctx.ui?.warn?.('No indexes built yet. Run `kb mind index` first.');
          return { ok: true, result: res };
        }

        ctx.ui?.success?.('Mind indexes', {
          sections: [
            {
              header: `${res.indexes.length} index(es)`,
              items: res.indexes.map((i: IndexSummary) => {
                const head = `${i.indexId}${i.label ? ` — ${i.label}` : ''}`;
                const counts = `${i.documents} doc(s), ${i.chunks} chunk(s)`;
                const scope = i.coverage ? `\n    scope: ${i.coverage}` : '';
                const stale = i.staleCount ? `, ${i.staleCount} stale` : '';
                const updated = i.lastIndexedAt ? `, updated ${i.lastIndexedAt}` : ' (not built)';
                return `${head}: ${counts}${stale}${updated}${scope}`;
              }),
            },
          ],
        });
        return { ok: true, result: res };
      } catch (err) {
        return handleError(ctx, err, flags.json);
      }
    },
  },
});
