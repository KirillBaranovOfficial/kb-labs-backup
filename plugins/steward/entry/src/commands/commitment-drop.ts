import { defineCommand, handleError, validationError, type CLIInput, type PluginCommandContextV3 } from '@kb-labs/sdk';
import { commitmentDrop } from '@kb-labs/steward-core';

type Flags = { reason?: string; json?: boolean };

export default defineCommand({
  id: 'steward:commitment.drop',
  description: 'Cancel a commitment — reason is required and recorded in history',

  handler: {
    async execute(ctx: PluginCommandContextV3, input: CLIInput<Flags>) {
      const [id] = input.argv;
      const { reason, json } = input.flags;
      if (!id || !reason?.trim()) {
        return validationError(ctx, 'id and --reason are required', 'Usage: kb steward commitment drop <id> --reason=...', json);
      }

      try {
        const commitment = await commitmentDrop({ id, reason });
        if (!commitment) {
          return { ok: false, error: { code: 'KB_RUNTIME_RESOURCE_NOT_FOUND', message: `No commitment "${id}"`, hint: "Run `kb steward commitment drop --help` to review the failure and next steps." }, result: null };
        }
        if (json) {return { ok: true, result: commitment };}
        else {ctx.ui?.info?.(`Dropped: "${commitment.text}" (${reason})`);}
        return { ok: true, result: commitment };
      } catch (err) {
        ctx.platform.logger?.error?.('steward:commitment.drop failed', err instanceof Error ? err : new Error(String(err)));
        return handleError(ctx, err, json);
      }
    },
  },
});
