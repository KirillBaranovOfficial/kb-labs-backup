import { expect, it } from 'vitest';
import './test-utils.js';
import foldersCommand from '../../commands/mail-folders.js';
import { context } from './test-utils.js';

it('returns folders as JSON', async () => {
  await expect(foldersCommand.execute(context as never, { argv: [], flags: { json: true } } as never)).resolves.toEqual([]);
});
