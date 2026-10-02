import { describe, expect, it } from 'vitest';
import { createUnknownCommandError } from './bootstrap.js';

describe('CLI unknown command errors', () => {
  // case: S-203.2
  it('maps an unknown command to a catalogued actionable KB error', () => {
    expect(createUnknownCommandError('workflow lint', ['workflow defs list'])).toMatchObject({
      code: 'KB_RUNTIME_INPUT_INVALID',
      message: 'Unknown command: workflow lint',
      hint: 'Check the command name or run "kb --help" to list available commands.',
      details: { command: 'workflow lint', suggestions: 'workflow defs list' },
    });
  });
});
