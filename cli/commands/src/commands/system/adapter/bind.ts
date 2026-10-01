/** Bind an installed machine adapter to a slot in the current project's config. */

import { configErrorEnvelope, setUserConfigValue } from '@kb-labs/core-config';
import { DiagnosticCollector, readMarketplaceLock } from '@kb-labs/core-discovery';
import { PlatformUserConfigSchema } from '@kb-labs/core-runtime';
import { defineSystemCommand, type CommandResult } from '@kb-labs/shared-command-kit';
import { getContextCwd } from '@kb-labs/shared-cli-ui';
import { generateExamples } from '../../../utils/generate-examples';
import { renderErrorEnvelope, resolveConfigRoots, userConfigFileForScope } from '../config/shared';

type Flags = {
  json: { type: 'boolean'; description?: string };
  'dry-run': { type: 'boolean'; description?: string };
};

type BindResult = CommandResult & {
  slot?: string;
  packageId?: string;
  file?: string;
  changed?: boolean;
  dryRun?: boolean;
  hint?: string;
  envelope?: ReturnType<typeof configErrorEnvelope>;
};

export const adapterBind = defineSystemCommand<Flags, BindResult>({
  name: 'bind',
  description: 'Bind an installed platform adapter to a project slot',
  longDescription:
    'Writes platform.adapters.<slot> in the current project configuration. The adapter package must first be installed in the machine platform.',
  category: 'adapter',
  examples: generateExamples('adapter bind', 'kb', [
    { flags: {}, description: 'storage @kb-labs/adapters-fs' },
    { flags: { 'dry-run': true, json: true }, description: 'llm @kb-labs/adapters-openai' },
  ]),
  flags: {
    json: { type: 'boolean', description: 'Output machine-readable JSON' },
    'dry-run': { type: 'boolean', description: 'Validate without writing project config' },
  },
  analytics: {
    command: 'adapter.bind',
    startEvent: 'ADAPTER_BIND_STARTED',
    finishEvent: 'ADAPTER_BIND_FINISHED',
  },
  async handler(ctx, argv, flags) {
    const [slot, packageId] = argv;
    if (!slot || !/^[a-z][a-zA-Z0-9-]*$/.test(slot) || !packageId) {
      return { ok: false, error: 'Provide a valid adapter slot and an installed package ID.' };
    }

    try {
      const roots = await resolveConfigRoots(getContextCwd(ctx));
      const lock = await readMarketplaceLock(roots.platformRoot, new DiagnosticCollector());
      if (!lock?.adapters?.[packageId]) {
        return {
          ok: false,
          error: `Adapter package ${packageId} is not installed on this platform.`,
          hint: `Run: kb marketplace install ${packageId} --scope platform`,
        };
      }

      const result = await setUserConfigValue({
        filePath: await userConfigFileForScope(roots, 'project'),
        path: ['platform', 'adapters', slot],
        value: packageId,
        schema: PlatformUserConfigSchema,
        dryRun: Boolean(flags['dry-run']),
      });
      return {
        ok: true,
        slot,
        packageId,
        file: result.filePath,
        changed: result.changed,
        dryRun: result.dryRun,
      };
    } catch (error) {
      const envelope = configErrorEnvelope(error);
      return { ok: false, error: envelope.message, envelope };
    }
  },
  formatter(result, ctx, flags) {
    if (!result.ok && result.envelope) {
      renderErrorEnvelope(ctx, result.envelope, Boolean(flags.json));
      return;
    }
    if (flags.json) {
      ctx.ui?.json?.(result.ok ? result : { ok: false, error: result.error, hint: result.hint });
      return;
    }
    if (!result.ok) {
      ctx.ui?.error(`${result.error}\n${result.hint ?? ''}`.trim());
      return;
    }
    ctx.ui?.success(result.dryRun ? 'Adapter binding is valid; no config written' : 'Adapter bound to project', {
      sections: [{ header: 'Adapter', items: [`Slot: ${result.slot}`, `Package: ${result.packageId}`, `Config: ${result.file}`] }],
    });
  },
});
