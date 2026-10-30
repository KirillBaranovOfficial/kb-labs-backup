import { expect, it } from 'vitest';
import './test-utils.js';
import markCommand from '../../commands/mail-mark.js';
import { context } from './test-utils.js';

it('marks a message and returns the selected action as JSON', async () => {
  await expect(markCommand.execute(context as never, { argv: ['1'], flags: { json: true, read: true } } as never)).resolves.toEqual({ uid: 1, action: 'read' });
});
