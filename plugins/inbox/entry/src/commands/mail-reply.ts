import { defineCommand, validationError, type CLIInput, type PluginContextV3 } from '@kb-labs/sdk';
import { resolveAccount, getMessage, replyMessage } from '@kb-labs/inbox-core';
import { handleError } from '../utils/error.js';

type ReplyFlags = {
  body?: string;
  folder?: string;
  account?: string;
  json?: boolean;
};

export default defineCommand({
  id: 'inbox:mail.reply',
  description: 'Reply to an email by uid (preserves thread)',

  handler: {
    async execute(ctx: PluginContextV3, input: CLIInput<ReplyFlags>) {
      const [rawUid] = input.argv;
      if (!rawUid) {
        return validationError(ctx, 'uid is required', 'Usage: kb inbox reply <uid> --body "..."', input.flags.json);
      }
      if (!input.flags.body) {
        return validationError(ctx, '--body is required', undefined, input.flags.json);
      }

      const uid = parseInt(rawUid, 10);
      if (isNaN(uid)) {
        return validationError(ctx, `Invalid uid: "${rawUid}" — must be a number`, undefined, input.flags.json);
      }

      try {
        const account = resolveAccount(input.flags.account);
        const folder = input.flags.folder ?? 'INBOX';

        const original = await getMessage(account, uid, { folder });
        const result = await replyMessage(account, {
          body: input.flags.body,
          originalMessageId: original.messageId,
          originalReferences: original.references,
          originalSubject: original.subject,
        }, input.flags.body);

        if (input.flags.json) {
          return result;
        }

        ctx.ui?.success?.(`Reply sent  id: ${result.messageId}`);
          return result;
      } catch (err) {
        return handleError(ctx, err, input.flags.json);
      }
    },
  },
});
