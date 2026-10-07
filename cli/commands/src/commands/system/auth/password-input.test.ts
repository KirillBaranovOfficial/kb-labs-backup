import { describe, expect, it, vi } from 'vitest';
import { Readable } from 'node:stream';
import { promptForPassword, readPasswordFromStdin } from './password-input.js';

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

  // case: S-041.6
  it('uses the actual login values in the non-TTY command hint', async () => {
    vi.stubEnv('CI', '1');
    const command = "cat password.txt | kb auth login --gateway-url 'https://gateway.example.test' --email 'member@example.test' --password-stdin";
    try {
      await expect(promptForPassword(command)).rejects.toMatchObject({
        code: 'KB_RUNTIME_INPUT_INVALID',
        hint: command,
        cause: 'The password prompt requires an interactive terminal.',
      });
    } finally {
      vi.unstubAllEnvs();
    }
  });
});
