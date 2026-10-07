import { defineCommand, validationError, type CLIInput, type PluginContextV3 } from '@kb-labs/sdk';
import { resolveAccount, deleteMessage } from '@kb-labs/inbox-core';
import { handleError } from '../utils/error.js';

type DeleteFlags = {
  folder?: string;
  account?: string;
  json?: boolean;
};

export default defineCommand({
  id: 'inbox:mail.delete',
  description: 'Delete an email (moves to Trash)',

  handler: {
    async execute(ctx: PluginContextV3, input: CLIInput<DeleteFlags>) {
      const [rawUid] = input.argv;
      if (!rawUid) {
        return validationError(ctx, 'uid is required', 'Usage: kb inbox delete <uid>', input.flags.json);
      }

      const uid = parseInt(rawUid, 10);
      if (isNaN(uid)) {
        return validationError(ctx, `Invalid uid: "${rawUid}" — must be a number`, undefined, input.flags.json);
      }

      try {
        const account = resolveAccount(input.flags.account);
        await deleteMessage(account, uid, input.flags.folder ?? 'INBOX');

        if (input.flags.json) {
          return { uid, deleted: true };
        }

        ctx.ui?.success?.(`Deleted uid=${uid}`);
        return null;
      } catch (err) {
        return handleError(ctx, err, input.flags.json);
      }
    },
  },
});
