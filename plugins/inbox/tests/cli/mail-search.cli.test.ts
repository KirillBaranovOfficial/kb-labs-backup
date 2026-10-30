import { expect, it } from 'vitest';
import './test-utils.js';
import searchCommand from '../../src/commands/mail-search.js';
import { context } from './test-utils.js';

it('returns search results as JSON', async () => {
  await expect(searchCommand.execute(context as never, { argv: [], flags: { json: true, text: 'invoice' } } as never)).resolves.toEqual([]);
});
