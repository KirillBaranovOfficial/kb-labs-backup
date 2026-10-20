import { describe, expect, it } from 'vitest';
import { createUnknownCommandError } from './bootstrap.js';
import { cliBinaryName } from './cli-binary.js';
import manifest from '../../package.json' with { type: 'json' };

describe('CLI unknown command errors', () => {
  it('uses the binary name declared in the CLI package registry', () => {
    expect(cliBinaryName).toBe(Object.keys(manifest.bin)[0]);
  });
  // case: S-203.2, S-047.3
  it('maps a removed plugin command to a catalogued actionable KB error', () => {
    expect(createUnknownCommandError('workflow lint', ['workflow defs list'])).toMatchObject({
      code: 'KB_RUNTIME_UNKNOWN_COMMAND',
      message: 'Unknown command: workflow lint',
      hint: expect.stringContaining(`${cliBinaryName} --help`),
      details: { command: 'workflow lint', suggestions: 'workflow defs list' },
    });
  });

  // case: S-203.21
  it('names discovery failures and their exact doctor command in human and JSON errors', () => {
    const error = createUnknownCommandError('journey analytics emit', [], [{
      severity: 'error',
      code: 'COMMAND_HANDLER_MISSING',
      message: 'Handler missing',
      context: { pluginId: '@kb-labs/e2e-analytics-emitter' },
      ts: 0,
      remediation: 'Build @kb-labs/e2e-analytics-emitter, then run "kb marketplace plugins doctor --scope project".',
    }]);

    expect(error.hint).toContain('@kb-labs/e2e-analytics-emitter (COMMAND_HANDLER_MISSING)');
    expect(error.hint).toContain('doctor --scope project');
    expect(error.hint).toContain(`${cliBinaryName} --help`);
    expect(error.details).toMatchObject({
      command: 'journey analytics emit',
      pluginDiagnostics: '@kb-labs/e2e-analytics-emitter:COMMAND_HANDLER_MISSING',
    });
  });
});
