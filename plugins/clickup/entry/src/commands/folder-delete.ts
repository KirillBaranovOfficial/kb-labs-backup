import { defineCommand, type CLIInput, type PluginCommandContextV3 } from '@kb-labs/sdk';
import { requireApiKey, deleteFolder } from '@kb-labs/clickup-core';
import { handleError, validationError } from '../utils/error.js';

type FolderDeleteFlags = {
  force?: boolean;
  json?: boolean;
  'dry-run'?: boolean;
};

export default defineCommand({
  id: 'clickup:folder.delete',
  description: 'Delete a folder',

  handler: {
    async intent(_ctx: PluginCommandContextV3, input: CLIInput<FolderDeleteFlags>) {
      const [folderId] = input.argv;
      return {
        summary: `Delete folder ${folderId ?? '(unknown)'}`,
        operations: [{ type: 'delete' as const, resource: 'folder', details: { folderId } }],
      };
    },

    async execute(ctx: PluginCommandContextV3, input: CLIInput<FolderDeleteFlags>) {
      const folderId = input.argv[0] as string | undefined;
      if (!folderId) {
        return validationError(ctx, 'folderId is required', 'Usage: kb clickup folder delete <folderId> --force', input.flags.json);
      }
      if (!input.flags.force) {
        return validationError(ctx, `--force is required to delete folder ${folderId}`, 'Add --force to confirm deletion', input.flags.json);
      }

      try {
        await deleteFolder(requireApiKey(), folderId);

        if (input.flags.json) {
          return { ok: true, result: { deleted: true, folderId } };
        } else {
          ctx.ui?.success?.(`Deleted folder ${folderId}`);
        }

        return { ok: true, result: { deleted: true, folderId } };
      } catch (err) {
        return handleError(ctx, err, input.flags.json);
      }
    },
  },
});
