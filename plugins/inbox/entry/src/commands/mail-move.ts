import { defineCommand, validationError, type CLIInput, type PluginContextV3 } from '@kb-labs/sdk';
import { resolveAccount, moveMessage } from '@kb-labs/inbox-core';
import { handleError } from '../utils/error.js';

type MoveFlags = {
  folder?: string;
  from?: string;
  account?: string;
  json?: boolean;
};

export default defineCommand({
  id: 'inbox:mail.move',
  description: 'Move an email to a folder',

  handler: {
    async execute(ctx: PluginContextV3, input: CLIInput<MoveFlags>) {
      const [rawUid] = input.argv;
      if (!rawUid) {
        return validationError(ctx, 'uid is required', 'Usage: kb inbox move <uid> --folder Work', input.flags.json);
      }
      if (!input.flags.folder) {
        return validationError(ctx, '--folder is required', undefined, input.flags.json);
      }

      const uid = parseInt(rawUid, 10);
      if (isNaN(uid)) {
        return validationError(ctx, `Invalid uid: "${rawUid}" — must be a number`, undefined, input.flags.json);
      }

      try {
        const account = resolveAccount(input.flags.account);
        await moveMessage(account, uid, input.flags.from ?? 'INBOX', input.flags.folder);

        if (input.flags.json) {
          return { uid, folder: input.flags.folder };
        }

        ctx.ui?.success?.(`Moved uid=${uid} → ${input.flags.folder}`);
        return null;
      } catch (err) {
        return handleError(ctx, err, input.flags.json);
      }
    },
  },
});
