import { expect, it } from 'vitest';
import './test-utils.js';
import accountsCommand from '../../commands/mail-accounts.js';
import { context } from './test-utils.js';

it('returns configured accounts as JSON', async () => {
  await expect(accountsCommand.execute(context as never, { argv: [], flags: { json: true } } as never)).resolves.toEqual([]);
});
