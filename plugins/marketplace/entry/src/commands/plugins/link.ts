import { defineCommand, validationError, type PluginContextV3, type CommandResult } from '@kb-labs/sdk';
import { handleMarketplaceError } from '../../errors.js';
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
    async intent(_ctx: PluginContextV3, input: LinkInput) {
      const flags = (input.flags ?? input) as LinkFlags;
      const pluginPath = flags.link ?? input.argv?.[0] ?? '(unknown)';
      return {
        summary: `Link local plugin at "${pluginPath}"`,
        operations: [{ type: 'create' as const, resource: 'plugin-link', details: { path: pluginPath } }],
      };
    },

    async execute(ctx: PluginContextV3, input: LinkInput): Promise<CommandResult<{ id: string; scope: string }>> {
      const flags = (input.flags ?? input) as LinkFlags;
      const pluginPath = flags.link ?? input.argv?.[0];

      if (!pluginPath) {
        validationError(ctx, 'Specify a project-relative package path to link', 'Usage: kb marketplace link --link <relative-path>', flags.json);
        return { ok: false, error: 'A plugin path is required', result: { id: '', scope: '' } };
      }

      let scopeCtx;
      try {
        scopeCtx = await resolveCliScope(ctx.cwd, flags.scope);
      } catch (err) {
        if (err instanceof CliScopeError) {
          validationError(ctx, err.message, undefined, flags.json);
        } else {
          handleMarketplaceError(ctx, err, flags.json);
        }
        return { ok: false, error: err instanceof Error ? err.message : String(err), result: { id: '', scope: '' } };
      }

      try {
        // The path is relative to the resolved marketplace scope root. This
        // keeps platform adapter links usable without exposing machine paths.
        if (pluginPath.startsWith('/') || /^[A-Za-z]:[\\/]/.test(pluginPath)) {
          throw new Error('The --link path must be relative to the selected scope directory.');
        }
        const result = await post<LinkResponse>(`/packages/link`, { path: pluginPath, ...scopeBody(scopeCtx) });

        if (flags.json) {
          ctx.ui?.json?.({ ok: true, id: result.id, scope: result.scope });
        } else {
          ctx.ui?.success?.(`Linked ${result.id} (${result.scope})`);
        }
        return { ok: true, result: { id: result.id, scope: result.scope } };
      } catch (err) {
        handleMarketplaceError(ctx, err, flags.json);
        return { ok: false, error: err instanceof Error ? err.message : String(err), result: { id: '', scope: '' } };
      }
    },
  },
});
