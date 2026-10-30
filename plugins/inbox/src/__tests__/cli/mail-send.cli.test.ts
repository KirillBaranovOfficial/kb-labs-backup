import { expect, it } from 'vitest';
import './test-utils.js';
import sendCommand from '../../commands/mail-send.js';
import { context } from './test-utils.js';

it('sends a message and returns its message id as JSON', async () => {
  await expect(sendCommand.execute(context as never, { argv: [], flags: { json: true, to: 'user@example.com', subject: 'Hello', body: 'Message' } } as never)).resolves.toEqual({ messageId: 'sent' });
});
