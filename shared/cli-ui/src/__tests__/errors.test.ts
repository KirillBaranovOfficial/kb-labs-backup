import { describe, expect, it } from 'vitest';
import { handleError } from '../utils/errors.js';

describe('handleError', () => {
  it('preserves the catalog error envelope as a command failure for the shared renderer', () => {
    const envelope = {
      code: 'KB_AUTH_REQUIRED', area: 'auth', stage: 'run', severity: 'error', retryable: false,
      message: 'Authentication is required.', hint: 'Run "kb auth login".',
    };
    const error = Object.assign(new Error(envelope.message), { envelope });

    expect(handleError({ ui: {} }, error, true)).toMatchObject({
      ok: false,
      error: {
        code: 'KB_AUTH_REQUIRED',
        message: 'Authentication is required.',
        hint: 'Run "kb auth login".',
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
