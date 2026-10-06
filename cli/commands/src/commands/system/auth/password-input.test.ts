import { describe, expect, it } from 'vitest';
import { Readable } from 'node:stream';
import { readPasswordFromStdin } from './password-input.js';

describe('auth login password input', () => {
  // case: S-041.6
  it('reads one password from stdin without changing its contents', async () => {
    await expect(readPasswordFromStdin(Readable.from([' secret with spaces \r\n'])))
      .resolves.toBe(' secret with spaces ');
  });

  // case: S-041.6
  it('accepts stdin ending without a newline', async () => {
    await expect(readPasswordFromStdin(Readable.from(['secret'])))
      .resolves.toBe('secret');
  });
});
