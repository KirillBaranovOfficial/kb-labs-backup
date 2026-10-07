import { defineCommand, validationError, handleError, type PluginCommandContextV3, type CommandResult } from '@kb-labs/sdk';

import { post } from '../../http.js';
import { resolveCliScope, scopeBody, CliScopeError } from '../../scope.js';

interface LinkFlags {
  link?: string;
  scope?: string;
  json?: boolean;
  'dry-run'?: boolean;
}

interface LinkInput {
  argv?: string[];
  flags?: LinkFlags;
}

interface LinkResponse {
  id: string;
  version: string;
  primaryKind: string;
  provides: string[];
  packageRoot: string;
  scope: string;
}

export default defineCommand<unknown, LinkInput, { id: string; scope: string }>({
  id: 'marketplace:link',
  description: 'Link a local plugin for development',

  handler: {
    async intent(_ctx: PluginCommandContextV3, input: LinkInput) {
      const flags = (input.flags ?? input) as LinkFlags;
      const pluginPath = flags.link ?? input.argv?.[0] ?? '(unknown)';
      return {
        summary: `Link local plugin at "${pluginPath}"`,
        operations: [{ type: 'create' as const, resource: 'plugin-link', details: { path: pluginPath } }],
      };
    },

    async execute(ctx: PluginCommandContextV3, input: LinkInput): Promise<CommandResult<{ id: string; scope: string }>> {
      const flags = (input.flags ?? input) as LinkFlags;
      const pluginPath = flags.link ?? input.argv?.[0];

      if (!pluginPath) {
        return validationError(ctx, 'Specify a project-relative package path to link', 'Usage: kb marketplace link --link <relative-path>', flags.json);
      }

      let scopeCtx;
      try {
        scopeCtx = await resolveCliScope(ctx.cwd, flags.scope);
      } catch (err) {
        if (err instanceof CliScopeError) {
          return validationError(ctx, err.message, undefined, flags.json);
        } else {
          return handleError(ctx, err, flags.json);
        }
              }

      // Path validation uses the shared validation presenter, so the human
      // message remains actionable without treating bad input as a runtime error.
      if (pluginPath.startsWith('/') || /^[A-Za-z]:[\\/]/.test(pluginPath)) {
        return validationError(
          ctx,
          'The --link path must be relative to the selected scope directory.',
          'Provide a path relative to the selected scope directory, for example `.kb/plugins/demo/packages/demo-entry`.',
          flags.json,
        );
      }

      try {
        // The path is relative to the resolved marketplace scope root. This
        // keeps platform adapter links usable without exposing machine paths.
        const result = await post<LinkResponse>(`/packages/link`, { path: pluginPath, ...scopeBody(scopeCtx) });

        if (flags.json) {
          return { id: result.id, scope: result.scope };
        } else {
          ctx.ui?.success?.(`Linked ${result.id} (${result.scope})`);
        }
        return { id: result.id, scope: result.scope };
      } catch (err) {
        return handleError(ctx, err, flags.json);
      }
    },
  },
});
