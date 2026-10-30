import { expect, it } from 'vitest';
import './test-utils.js';
import getCommand from '../../commands/mail-get.js';
import { context } from './test-utils.js';

it('returns the requested message as JSON', async () => {
  await expect(getCommand.execute(context as never, { argv: ['1'], flags: { json: true } } as never)).resolves.toEqual({});
});
