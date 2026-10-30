import { expect, it } from 'vitest';
import './test-utils.js';
import threadCommand from '../../src/commands/mail-thread.js';
import { context } from './test-utils.js';

it('returns a thread as JSON', async () => {
  await expect(threadCommand.execute(context as never, { argv: ['1'], flags: { json: true } } as never)).resolves.toEqual([]);
});
