import { describe, expect, it, vi } from 'vitest';
import { confirm, multiSelect, select, text } from '../interactive/index.js';
import { NOOP_PROMPTS } from '../stdout-ui.js';

describe('interactive input boundary', () => {
    it('returns one typed required-input error for every prompt without a TTY', async () => {
    vi.stubEnv('CI', '1');
    const prompts = [
      () => confirm({ message: 'Continue?' }),
      () => text({ message: 'Name' }),
      () => select({ message: 'Choose', choices: [{ label: 'one', value: 'one' }] }),
      () => multiSelect({ message: 'Choose', choices: [{ label: 'one', value: 'one' }] }),
    ];

    try {
      for (const prompt of prompts) {
        await expect(prompt()).rejects.toMatchObject({
          code: 'KB_RUNTIME_INPUT_INVALID',
          cause: expect.any(String),
          hint: expect.any(String),
        });
      }
    } finally {
      vi.unstubAllEnvs();
    }
  });

  it('does not substitute defaults for prompts in non-interactive UI contexts', async () => {
    vi.stubEnv('CI', '1');
    try {
      const prompts = [
        () => NOOP_PROMPTS.confirm('Confirm?'),
        () => NOOP_PROMPTS.prompt('Project name'),
        () => NOOP_PROMPTS.select('Environment', [{ label: 'dev', value: 'dev' }]),
        () => NOOP_PROMPTS.multiSelect('Plugins', [{ label: 'workflow', value: 'workflow' }]),
      ];
      for (const prompt of prompts) {
        await expect(prompt()).rejects.toMatchObject({
          code: 'KB_RUNTIME_INPUT_INVALID',
          cause: expect.any(String),
          hint: expect.any(String),
        });
      }
    } finally {
      vi.unstubAllEnvs();
    }
  });
});
