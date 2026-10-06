import { defineCommand, handleError, validationError, type CLIInput, type PluginCommandContextV3 } from '@kb-labs/sdk';
import { commitmentSnooze } from '@kb-labs/steward-core';
import { parseDate } from '../utils/flags.js';

type Flags = { until?: string; reason?: string; json?: boolean };

export default defineCommand({
  id: 'steward:commitment.snooze',
  description: "Postpone a commitment's reminder without cancelling it",

  handler: {
    async execute(ctx: PluginCommandContextV3, input: CLIInput<Flags>) {
      const [id] = input.argv;
      const { until, reason, json } = input.flags;
      if (!id || !until?.trim()) {
        return validationError(ctx, 'id and --until are required', 'Usage: kb steward commitment snooze <id> --until=...', json);
      }

      try {
        const commitment = await commitmentSnooze({ id, until: parseDate(until), reason });
        if (!commitment) {
          return { ok: false, error: { code: 'KB_RUNTIME_RESOURCE_NOT_FOUND', message: `No commitment "${id}"`, hint: "Run `kb steward commitment snooze --help` to review the failure and next steps." }, result: null };
        }
        if (json) {return { ok: true, result: commitment };}
        else {ctx.ui?.info?.(`Snoozed "${commitment.text}" until ${until}`);}
        return { ok: true, result: commitment };
      } catch (err) {
        ctx.platform.logger?.error?.('steward:commitment.snooze failed', err instanceof Error ? err : new Error(String(err)));
        return handleError(ctx, err, json);
      }
    },
  },
});
