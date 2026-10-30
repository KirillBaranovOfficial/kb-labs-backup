import { expect, it } from 'vitest';
import './test-utils.js';
import listCommand from '../../commands/mail-list.js';
import { context } from './test-utils.js';

it('returns messages as JSON', async () => {
  await expect(listCommand.execute(context as never, { argv: [], flags: { json: true } } as never)).resolves.toEqual([]);
});
