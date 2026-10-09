import { defineCommand, validationError, handleError, type PluginCommandContextV3, type CommandResult } from '@kb-labs/sdk';

import { post } from '../../http.js';
import { resolveCliScope, scopeBody, CliScopeError } from '../../scope.js';

interface UnlinkFlags {
  json?: boolean;
  scope?: string;
  'dry-run'?: boolean;
}

interface UnlinkInput {
  argv?: string[];
  flags?: UnlinkFlags;
}

export default defineCommand<unknown, UnlinkInput, { packageId: string; scope: string }>({
  id: 'marketplace:plugins:unlink',
  description: 'Unlink a plugin',

  handler: {
    async intent(_ctx: PluginCommandContextV3, input: UnlinkInput) {
      const packageId = input.argv?.[0] ?? '(unknown)';
      return {
        summary: `Unlink plugin "${packageId}"`,
        operations: [{ type: 'delete' as const, resource: 'plugin-link', details: { packageId } }],
      };
    },

    async execute(ctx: PluginCommandContextV3, input: UnlinkInput): Promise<CommandResult<{ packageId: string; scope: string }>> {
      const packageId = input.argv?.[0];
      const flags = (input.flags ?? input) as UnlinkFlags;

      if (!packageId) {
        return validationError('Specify a package ID to unlink', 'Usage: kb marketplace plugins unlink <plugin-id>');
      }

      let scopeCtx;
      try {
        scopeCtx = await resolveCliScope(ctx.cwd, flags.scope);
      } catch (err) {
        if (err instanceof CliScopeError) {
          return validationError(err.message);
        } else {
          return handleError(err);
        }
              }

      try {
        await post(`/packages/unlink`, { packageId, ...scopeBody(scopeCtx) });
        if (flags.json) {
          return { packageId, scope: scopeCtx.scope };
        } else {
          ctx.ui?.success?.(`Unlinked ${packageId} (${scopeCtx.scope})`);
        }
        return { packageId, scope: scopeCtx.scope };
      } catch (err) {
        return handleError(err);
      }
    },
  },
});
