import { defineCommand, handleError, validationError, type CLIInput, type PluginCommandContextV3 } from '@kb-labs/sdk';
import { getPerson } from '@kb-labs/steward-core';

type Flags = { json?: boolean };

export default defineCommand({
  id: 'steward:person.get',
  description: 'Get a contact by id or exact name',

  handler: {
    async execute(ctx: PluginCommandContextV3, input: CLIInput<Flags>) {
      const [idOrName] = input.argv;
      const { json } = input.flags;
      if (!idOrName) {
        return validationError(ctx, 'idOrName is required', 'Usage: kb steward person get <idOrName>', json);
      }

      try {
        const person = await getPerson(idOrName);
        if (!person) {
          return { ok: false, error: { code: 'KB_RUNTIME_RESOURCE_NOT_FOUND', message: `No person matching "${idOrName}"`, hint: "Run `kb steward person get --help` to review the failure and next steps." }, result: null };
        }

        if (json) {return { ok: true, result: person };}
        else {ctx.ui?.info?.(`${person.name} — topics: ${person.globalTopics.join(', ') || '(none)'}`);}
        return { ok: true, result: person };
      } catch (err) {
        ctx.platform.logger?.error?.('steward:person.get failed', err instanceof Error ? err : new Error(String(err)));
        return handleError(ctx, err, json);
      }
    },
  },
});
