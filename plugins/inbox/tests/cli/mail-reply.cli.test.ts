import { expect, it } from 'vitest';
import './test-utils.js';
import replyCommand from '../../src/commands/mail-reply.js';
import { context } from './test-utils.js';

it('sends a reply and returns its message id as JSON', async () => {
  await expect(replyCommand.execute(context as never, { argv: ['1'], flags: { json: true, body: 'Reply' } } as never)).resolves.toEqual({ messageId: 'replied' });
});
