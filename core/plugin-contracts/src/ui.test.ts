import { describe, expect, it } from 'vitest';
import { InteractiveInputRequiredError, noopUI } from './ui.js';

describe('noopUI prompt contract', () => {
  // case: S-215.1
  it('rejects every required prompt instead of substituting a default', async () => {
    const prompts = [
      () => noopUI.confirm('Confirm?'),
      () => noopUI.prompt('Project name', { default: 'demo' }),
      () => noopUI.select('Environment', [{ label: 'dev', value: 'dev' }]),
      () => noopUI.multiSelect('Plugins', [{ label: 'workflow', value: 'workflow', checked: true }]),
    ];

    for (const prompt of prompts) {
      const error = await prompt().catch((failure: unknown) => failure);
      expect(error).toBeInstanceOf(InteractiveInputRequiredError);
      expect(Object.keys(error as InteractiveInputRequiredError).sort()).toEqual(['cause', 'code', 'hint']);
      expect(error).toMatchObject({
        code: 'KB_RUNTIME_INPUT_INVALID',
        hint: 'Provide the value as workflow or request input, then run `kb workflow run --help`.',
        cause: expect.any(String),
      });
    }
  });
});
