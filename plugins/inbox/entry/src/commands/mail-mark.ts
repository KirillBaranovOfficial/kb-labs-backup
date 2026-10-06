import { defineCommand, validationError, type CLIInput, type PluginContextV3 } from '@kb-labs/sdk';
import { resolveAccount, markMessage, type MarkAction } from '@kb-labs/inbox-core';
import { handleError } from '../utils/error.js';

type MarkFlags = {
  read?: boolean;
  unread?: boolean;
  spam?: boolean;
  flagged?: boolean;
  unflagged?: boolean;
  folder?: string;
  account?: string;
  json?: boolean;
};

export default defineCommand({
  id: 'inbox:mail.mark',
  description: 'Mark an email as read, unread, spam, or flagged',

  handler: {
    async execute(ctx: PluginContextV3, input: CLIInput<MarkFlags>) {
      const [rawUid] = input.argv;
      if (!rawUid) {
        return validationError(ctx, 'uid is required', 'Usage: kb inbox mark <uid> --read|--unread|--spam|--flagged|--unflagged', input.flags.json);
      }

      const uid = parseInt(rawUid, 10);
      if (isNaN(uid)) {
        return validationError(ctx, `Invalid uid: "${rawUid}" — must be a number`, undefined, input.flags.json);
      }

      const action: MarkAction | undefined =
        input.flags.read      ? 'read'      :
        input.flags.unread    ? 'unread'    :
        input.flags.spam      ? 'spam'      :
        input.flags.flagged   ? 'flagged'   :
        input.flags.unflagged ? 'unflagged' :
        undefined;

      if (!action) {
        return validationError(ctx, 'One of --read, --unread, --spam, --flagged, --unflagged is required', undefined, input.flags.json);
      }

      try {
        const account = resolveAccount(input.flags.account);
        await markMessage(account, uid, input.flags.folder ?? 'INBOX', action);

        if (input.flags.json) {
          return { ok: true, result: { uid, action } };
        }

        ctx.ui?.success?.(`Marked uid=${uid} as ${action}`);
        return { ok: true, result: null };
      } catch (err) {
        return handleError(ctx, err, input.flags.json);
      }
    },
  },
});
