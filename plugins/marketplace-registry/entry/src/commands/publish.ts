import * as path from 'node:path';
import * as fs from 'node:fs/promises';
import { defineCommand, handleError, validationError, type CLIInput, type PluginCommandContextV3, type CommandResult } from '@kb-labs/sdk';
import { registryPostMultipart, registryPatch, resolveRegistryHandle } from '../registry-http.js';
import { packPlugin } from '../pack-plugin.js';

interface PublishFlags {
  private?: boolean;
  path?: string;
  metaOnly?: boolean;
  json?: boolean;
}

interface PublishResultData {
  handle: string;
  name: string;
  version: string;
  visibility: string;
  trust: string;
  installCommand: string;
  pageUrl?: string;
}

export default defineCommand<unknown, CLIInput<PublishFlags>, PublishResultData>({
  id: 'marketplace:publish',
  description: 'Publish plugin to KB Labs Registry',

  handler: {
    async execute(ctx: PluginCommandContextV3, input: CLIInput<PublishFlags>): Promise<CommandResult<PublishResultData>> {
      const { flags = {} } = input;
      const pluginDir = path.resolve(ctx.cwd, flags.path ?? '.');
      const visibility = flags.private ? 'private' : 'public';
      const t0 = Date.now();

      let pkgJson: Record<string, unknown>;
      try {
        const content = await fs.readFile(path.join(pluginDir, 'package.json'), 'utf-8');
        pkgJson = JSON.parse(content) as Record<string, unknown>;
      } catch {
        return validationError(ctx, 'Cannot read package.json in current directory', 'Run this command from the plugin root directory', flags.json);
      }

      if (!pkgJson.name || !pkgJson.version) {
        return validationError(ctx, 'package.json must have name and version fields', undefined, flags.json);
      }

      const kb = pkgJson['kb'] as Record<string, unknown> | undefined;
      if (!kb?.manifest && !kb?.adapter) {
        return validationError(
          ctx,
          'Not a KB Labs plugin or adapter',
          'Add a kb.manifest or kb.adapter field to package.json',
          flags.json,
        );
        return { ok: false, error: { code: 'KB_RUNTIME_COMMAND_FAILED', message: 'Not a KB Labs plugin or adapter', hint: "Run `kb hub publish --help` to review its inputs and recovery steps, then retry." } };
      }

      // Read README (non-fatal)
      let readme: string | undefined;
      for (const candidate of ['README.md', 'README.mdx', 'readme.md', 'Readme.md']) {
        readme = await fs.readFile(path.join(pluginDir, candidate), 'utf-8').catch(() => undefined);
        if (readme) break;
      }

      // Extract manifest fields for the detail page
      const manifest = (kb?.manifest ?? {}) as Record<string, unknown>;
      const manifestCommands = Array.isArray(manifest['commands'])
        ? (manifest['commands'] as Array<{ id?: string; name?: string }>).map(c => c.id ?? c.name ?? '').filter(Boolean)
        : undefined;

      const meta = {
        name: pkgJson.name as string,
        version: pkgJson.version as string,
        description: pkgJson.description as string | undefined,
        author: pkgJson.author as unknown,
        repository: pkgJson.repository as unknown,
        keywords: pkgJson.keywords as string[] | undefined,
        license: pkgJson.license as string | undefined,
        homepage: pkgJson.homepage as string | undefined,
        readme,
        permissions: manifest['permissions'] as string[] | undefined,
        envVars: manifest['envVars'] as string[] | undefined,
        allowedHosts: manifest['allowedHosts'] as string[] | undefined,
        commands: manifestCommands,
      };

      if (flags.metaOnly) {
        try {
          const handle = await resolveRegistryHandle();
          await registryPatch(`/packages/${handle}/${meta.name}/meta`, meta);
          ctx.ui?.success?.('Metadata updated', {
            sections: [{ header: 'Package', items: [`${meta.name} @ ${handle}`] }],
            timing: Date.now() - t0,
          });
          return { ok: true };
        } catch (err) {
          return handleError(ctx, err, flags.json);
        }
      }

      const primaryKind = kb?.adapter ? 'adapter' : 'plugin';

      const t1 = Date.now();
      let tarball: Buffer;
      let packMs: number;
      try {
        tarball = await packPlugin(pluginDir);
        packMs = Date.now() - t1;
      } catch (err) {
        return handleError(ctx, err, flags.json);
      }

      try {
        const result = await registryPostMultipart<PublishResultData>(
          '/packages/publish',
          tarball,
          { meta: JSON.stringify({ meta, visibility, primaryKind }) },
        );

        if (flags.json) {
          return { ok: true, result: result };
          return { ok: true, result };
        }

        ctx.ui?.chain?.([
          {
            title: `Packed ${meta.name}@${meta.version}`,
            timing: packMs,
          },
          {
            title: `${result.name}@${result.version} published`,
            status: 'success',
            summary: {
              'Visibility': result.visibility,
              'Trust': result.trust === 'trusted' ? 'KB Labs Platform ✓' : 'community',
              ...(result.pageUrl ? { 'Page': result.pageUrl } : {}),
            },
            sections: [{ header: 'Install', items: [{ text: result.installCommand, truncate: 500 }] }],
            timing: Date.now() - t0,
          },
        ]);

        return { ok: true, result };
      } catch (err) {
        if (flags.json) {
          return handleError(ctx, err, true);
        }
        const message = err instanceof Error ? err.message : String(err);
        ctx.ui?.chain?.([
          { title: `Packed ${meta.name}@${meta.version}`, timing: packMs },
          {
            title: 'Publish failed',
            status: 'error',
            sections: [{ items: [message] }],
            timing: Date.now() - t0,
          },
        ]);
        return { ok: false, error: { code: 'KB_RUNTIME_COMMAND_FAILED', message: message, hint: "Run `kb hub publish --help` to review its inputs and recovery steps, then retry." } };
      }
    },
  },
});
