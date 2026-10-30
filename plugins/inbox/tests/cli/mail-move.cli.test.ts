import { expect, it } from 'vitest';
import './test-utils.js';
import moveCommand from '../../src/commands/mail-move.js';
import { context } from './test-utils.js';

it('moves a message and returns its destination as JSON', async () => {
  await expect(moveCommand.execute(context as never, { argv: ['1'], flags: { json: true, folder: 'Archive' } } as never)).resolves.toEqual({ uid: 1, folder: 'Archive' });
});
