import { describe, expect, it } from 'vitest';
import { parseArgs } from '@kb-labs/cli-runtime';
import { createUnknownCommandError, resolveCliProjectScope } from './bootstrap.js';

describe('CLI platform scope resolution', () => {
  // case: S-041.7
  it('loads platform config for the secured marketplace command before the auth gate', () => {
    const parsed = parseArgs([
      'marketplace', 'plugins', 'list', '--scope', 'platform', '--json',
    ]);

    expect(resolveCliProjectScope(parsed)).toBe('platform');
  });

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
