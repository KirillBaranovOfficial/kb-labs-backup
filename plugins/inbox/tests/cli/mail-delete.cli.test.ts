import { expect, it } from 'vitest';
import './test-utils.js';
import deleteCommand from '../../src/commands/mail-delete.js';
import { context } from './test-utils.js';

it('deletes a message and returns its uid as JSON', async () => {
  await expect(deleteCommand.execute(context as never, { argv: ['1'], flags: { json: true } } as never)).resolves.toEqual({ uid: 1, deleted: true });
});
