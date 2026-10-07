/** Bind an installed machine adapter to a platform slot. */

import { configErrorEnvelope, createAdapterOptionsValidator, setUserConfigValue } from '@kb-labs/core-config';
import { PlatformUserConfigSchema } from '@kb-labs/core-runtime';
import { defineSystemCommand, throwSystemCataloguedError, throwSystemCommandError, type CommandResult } from '@kb-labs/shared-command-kit';
import { getContextCwd } from '@kb-labs/shared-cli-ui';
import { generateExamples } from '../../../utils/generate-examples';
import { resolveConfigRoots, userConfigFileForScope } from '../config/shared';

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
  scope: 'platform',
  jsonTest: { noSafeValidPath: "This command changes the platform adapter binding." },
  description: 'Bind an installed adapter to a platform slot',
  longDescription:
    'Writes platform.adapters.<slot> in the platform configuration. The adapter package must first be installed in the platform.',
  category: 'adapter',
  examples: generateExamples('adapter bind', 'kb', [
    { flags: {}, description: 'storage @kb-labs/adapters-fs' },
    { flags: { 'dry-run': true, json: true }, description: 'llm @kb-labs/adapters-openai' },
  ]),
  flags: {
    json: { type: 'boolean', description: 'Output machine-readable JSON' },
    'dry-run': { type: 'boolean', description: 'Validate without writing platform config' },
  },
  analytics: {
    command: 'adapter.bind',
    startEvent: 'ADAPTER_BIND_STARTED',
    finishEvent: 'ADAPTER_BIND_FINISHED',
  },
  async handler(ctx, argv, flags) {
    const [slot, packageId] = argv;
    if (!slot || !/^[a-z][a-zA-Z0-9-]*$/.test(slot) || !packageId) {
      throwSystemCommandError('Provide a valid adapter slot and an installed package ID.', "Run `kb adapter bind --help` to review valid inputs and recovery steps, then retry.");
    }

    let roots: Awaited<ReturnType<typeof resolveConfigRoots>>;
    try {
      roots = await resolveConfigRoots(getContextCwd(ctx));
    } catch (error) {
      throwSystemCataloguedError(configErrorEnvelope(error));
    }

    try {
      const validate = createAdapterOptionsValidator(['platform', 'adapters', slot], {
        platformRoot: roots.platformRoot,
      });
      const result = await setUserConfigValue({
        filePath: await userConfigFileForScope(roots, 'platform'),
        path: ['platform', 'adapters', slot],
        value: packageId,
        schema: PlatformUserConfigSchema,
        validate,
        dryRun: Boolean(flags['dry-run']),
      });
      return {

        slot,
        packageId,
        file: result.filePath,
        changed: result.changed,
        dryRun: result.dryRun,
      };
    } catch (error) {
      const envelope = configErrorEnvelope(error);
      throwSystemCataloguedError(envelope);
    }
  },
  formatter(result, ctx, flags) {
    ctx.ui?.success(result.dryRun ? 'Adapter binding is valid; no config written' : 'Adapter bound to platform', {
      sections: [{ header: 'Adapter', items: [`Slot: ${result.slot}`, `Package: ${result.packageId}`, `Config: ${result.file}`] }],
    });
  },
});
