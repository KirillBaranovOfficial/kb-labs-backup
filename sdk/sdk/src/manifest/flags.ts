import { defineFlags as commandKitDefineFlags } from '@kb-labs/shared-command-kit';
import type { FlagSchemaDefinition, FlagSchemaWithInfer } from '@kb-labs/shared-command-kit';
import type { CliFlagDecl } from '@kb-labs/plugin-contracts';

type ManifestFlagPayload<Flag = CliFlagDecl> = Flag extends CliFlagDecl ? Omit<Flag, 'name'> : never;

export function defineFlags<Definition extends FlagSchemaDefinition>(
  definition: Definition,
): FlagSchemaWithInfer<Definition> {
  return commandKitDefineFlags(definition);
}

export function defineCommandFlags<Definition extends Record<string, ManifestFlagPayload>>(
  definition: Definition,
): CliFlagDecl[] {
  return Object.entries(definition).map(([name, flag]) => ({ name, ...flag }));
}
