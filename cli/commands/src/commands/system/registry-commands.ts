import { defineSystemCommand, type CommandResult } from '@kb-labs/shared-command-kit';
import { registry } from '../../registry/service';

type RegistryCommandsFlags = {
  json: { type: 'boolean'; description?: string };
};

type RegistryCommandsResult = CommandResult & {
  commands: ReturnType<typeof registry.listProtectionCommands>;
};

export const registryCommands = defineSystemCommand<RegistryCommandsFlags, RegistryCommandsResult>({
  name: 'commands',
  scope: 'platform',
  jsonTest: { safeArgs: [] },
  description: 'List command paths and their authentication protection classes',
  category: 'registry',
  flags: { json: { type: 'boolean', description: 'Output in JSON format' } },
  async handler() {
    return { ok: true, commands: registry.listProtectionCommands() };
  },
});
