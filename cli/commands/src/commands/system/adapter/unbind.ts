/** Remove an adapter binding from the current project's user config. */

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
  description: 'Remove an adapter binding from the current project',
  longDescription:
    'Removes platform.adapters.<slot> from the current project configuration. Project adapter options are preserved.',
  category: 'adapter',
  examples: generateExamples('adapter unbind', 'kb', [
    { flags: {}, description: 'storage' },
    { flags: { 'dry-run': true, json: true }, description: 'llm' },
  ]),
  flags: {
    json: { type: 'boolean', description: 'Output machine-readable JSON' },
    'dry-run': { type: 'boolean', description: 'Validate without writing project config' },
  },
  analytics: { command: 'adapter.unbind', startEvent: 'ADAPTER_UNBIND_STARTED', finishEvent: 'ADAPTER_UNBIND_FINISHED' },
  async handler(ctx, argv, flags) {
    const [slot] = argv;
    if (!slot || !/^[a-z][a-z0-9-]*$/.test(slot)) {
      return { ok: false, error: 'Provide a lowercase adapter slot.' };
    }

    try {
      const roots = await resolveConfigRoots(getContextCwd(ctx));
      const result = await unsetUserConfigValue({
        filePath: await userConfigFileForScope(roots, 'project'),
        path: ['platform', 'adapters', slot],
        schema: PlatformUserConfigSchema,
        dryRun: Boolean(flags['dry-run']),
      });
      return { ok: true, slot, file: result.filePath, changed: result.changed, dryRun: result.dryRun };
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
    ctx.ui?.success(result.dryRun ? 'Adapter unbind validated; no config written' : 'Adapter unbound from project', {
      sections: [{ header: 'Adapter', items: [`Slot: ${result.slot}`, `Config: ${result.file}`] }],
    });
  },
});
