/** Remove an adapter binding from platform config. */

import { configErrorEnvelope, unsetUserConfigValue } from '@kb-labs/core-config';
import { PlatformUserConfigSchema } from '@kb-labs/core-runtime';
import { defineSystemCommand, type CommandResult } from '@kb-labs/shared-command-kit';
import { getContextCwd } from '@kb-labs/shared-cli-ui';
import { generateExamples } from '../../../utils/generate-examples';
import { renderErrorEnvelope, resolveConfigRoots, userConfigFileForScope } from '../config/shared';

type Flags = {
  json: { type: 'boolean'; description?: string };
  'dry-run': { type: 'boolean'; description?: string };
};

type UnbindResult = CommandResult & {
  slot?: string;
  file?: string;
  changed?: boolean;
  dryRun?: boolean;
  envelope?: ReturnType<typeof configErrorEnvelope>;
};

export const adapterUnbind = defineSystemCommand<Flags, UnbindResult>({
  name: 'unbind',
  scope: 'platform',
  jsonTest: { noSafeValidPath: "This command changes the platform adapter binding." },
  description: 'Remove an adapter binding from the platform',
  longDescription:
    'Removes platform.adapters.<slot> and platform.adapterOptions.<slot> from platform configuration.',
  category: 'adapter',
  examples: generateExamples('adapter unbind', 'kb', [
    { flags: {}, description: 'storage' },
    { flags: { 'dry-run': true, json: true }, description: 'llm' },
  ]),
  flags: {
    json: { type: 'boolean', description: 'Output machine-readable JSON' },
    'dry-run': { type: 'boolean', description: 'Validate without writing platform config' },
  },
  analytics: { command: 'adapter.unbind', startEvent: 'ADAPTER_UNBIND_STARTED', finishEvent: 'ADAPTER_UNBIND_FINISHED' },
  async handler(ctx, argv, flags) {
    const [slot] = argv;
    if (!slot || !/^[a-z][a-zA-Z0-9-]*$/.test(slot)) {
      return { ok: false, error: 'Provide a valid adapter slot.' };
    }

    try {
      const roots = await resolveConfigRoots(getContextCwd(ctx));
      const filePath = await userConfigFileForScope(roots, 'platform');
      const binding = await unsetUserConfigValue({
        filePath,
        path: ['platform', 'adapters', slot],
        schema: PlatformUserConfigSchema,
        dryRun: Boolean(flags['dry-run']),
      });
      const options = await unsetUserConfigValue({
        filePath,
        path: ['platform', 'adapterOptions', slot],
        schema: PlatformUserConfigSchema,
        dryRun: Boolean(flags['dry-run']),
      });
      return { ok: true, slot, file: binding.filePath, changed: binding.changed || options.changed, dryRun: binding.dryRun };
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
      ctx.ui?.json?.(result.ok ? result : { ok: false, error: result.error });
      return;
    }
    if (!result.ok) {
      ctx.ui?.error(`${result.error ?? 'Adapter unbind failed'}`);
      return;
    }
    ctx.ui?.success(result.dryRun ? 'Adapter unbind validated; no config written' : 'Adapter unbound from platform', {
      sections: [{ header: 'Adapter', items: [`Slot: ${result.slot}`, `Config: ${result.file}`] }],
    });
  },
});
