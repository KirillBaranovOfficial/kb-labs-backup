import { defineError, PluginError, commonErrors } from '@kb-labs/sdk';

export const ImpactError = defineError('IMPACT', {
  WorkspaceNotFound: {
    code: 500,
    message: () => 'Could not find workspace root',
    hint: 'Run the command from the root of the workspace you want to inspect.',
  },
  ConfigInvalid: {
    code: 500,
    message: (detail: string) => `Invalid impact config: ${detail}`,
    hint: 'Correct the impact configuration reported in the message, then retry.',
  },
});

export const CommonError = defineError('COMMON', commonErrors);

export { PluginError };
