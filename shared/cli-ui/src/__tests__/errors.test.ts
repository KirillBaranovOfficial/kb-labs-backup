import { describe, expect, it } from 'vitest';
import { handleError, renderErrorEnvelope } from '../utils/errors.js';

describe('handleError', () => {
  it('renders structured gate diagnostics as a list before the next-step hint', () => {
    expect(renderErrorEnvelope({
      code: 'KB_RUNTIME_COMMAND_FAILED',
      message: 'Validation failed.',
      hint: 'Fix the listed findings, then retry.',
      diagnostics: [{ code: 'QA_CASE_INVALID', message: 'Missing case claim.', severity: 'error', path: 'cases/S-1.md', line: 4 }],
    })).toContain('  - QA_CASE_INVALID (error) cases/S-1.md:4: Missing case claim.\n  Hint:  Fix the listed findings, then retry.');
  });

  // case: S-041.6, S-100.1
  it('renders the catalogued cause in human error output', () => {
    expect(renderErrorEnvelope({
      code: 'KB_RUNTIME_INPUT_INVALID',
      message: 'The provided input is invalid.',
      cause: 'The password prompt requires an interactive terminal.',
      hint: "cat password.txt | kb auth login --gateway-url 'http://gateway.test' --email 'member@example.test' --password-stdin",
    })).toContain(
      'The provided input is invalid. [KB_RUNTIME_INPUT_INVALID]\n' +
      '  Cause: The password prompt requires an interactive terminal.\n' +
      "  Hint:  cat password.txt | kb auth login --gateway-url 'http://gateway.test' --email 'member@example.test' --password-stdin",
    );
  });

  it('preserves the catalog error envelope as a command failure for the shared renderer', () => {
    const envelope = {
      code: 'KB_AUTH_REQUIRED', area: 'auth', stage: 'run', severity: 'error', retryable: false,
      message: 'Authentication is required.', hint: 'Run "kb auth login".',
      cause: 'No saved authentication session was found.',
    };
    const error = Object.assign(new Error(envelope.message), { envelope });

    expect(handleError({ ui: {} }, error, true)).toMatchObject({
      ok: false,
      error: {
        code: 'KB_AUTH_REQUIRED',
        message: 'Authentication is required.',
        hint: 'Run "kb auth login".',
        cause: 'No saved authentication session was found.',
      },
    });
  });

  it('maps unexpected errors to a command failure for the shared renderer', () => {
    expect(handleError({ ui: {} }, new Error('request failed'), true)).toMatchObject({
      ok: false,
      error: expect.objectContaining({
        code: 'KB_RUNTIME_UNEXPECTED',
        message: expect.any(String),
        hint: expect.any(String),
      }),
    });
  });

  it('preserves a permission carried by an authorization envelope in details', () => {
    const envelope = { code: 'KB_AUTH_FORBIDDEN', message: 'No permission.', hint: 'Ask an administrator.', details: { permission: 'auth:users:write' } };
    expect(handleError({ ui: {} }, Object.assign(new Error(envelope.message), { envelope }))).toMatchObject({
      ok: false,
      error: {
        code: 'KB_AUTH_FORBIDDEN',
        message: 'No permission.',
        hint: 'Ask an administrator.',
        details: { permission: 'auth:users:write' },
      },
    });
  });
});
