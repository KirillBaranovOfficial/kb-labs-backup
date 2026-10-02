import { describe, expect, it, vi } from 'vitest';
import { handleError } from '../utils/errors.js';

describe('handleError', () => {
  it('preserves the catalog error envelope in JSON and human output', () => {
    const json = vi.fn();
    const output = vi.fn();
    const envelope = {
      code: 'KB_AUTH_REQUIRED', area: 'auth', stage: 'run', severity: 'error', retryable: false,
      message: 'Authentication is required.', hint: 'Run "kb auth login".',
    };
    const error = Object.assign(new Error(envelope.message), { envelope });

    handleError({ ui: { json, error: output } }, error, true);

    expect(json).toHaveBeenCalledWith({ ok: false, error: envelope });

    handleError({ ui: { json, error: output } }, error);
    expect(output).toHaveBeenCalledWith('KB_AUTH_REQUIRED: Authentication is required.', {
      hint: 'Run "kb auth login".',
    });
  });

  it('maps unexpected errors to the shared platform error envelope', () => {
    const json = vi.fn();
    handleError({ ui: { json } }, new Error('request failed'), true);
    expect(json).toHaveBeenCalledWith(expect.objectContaining({
      ok: false,
      error: expect.objectContaining({
        code: 'KB_RUNTIME_UNEXPECTED',
        message: expect.any(String),
        hint: expect.any(String),
      }),
    }));
  });

  it('shows a permission carried by an authorization envelope in human output', () => {
    const output = vi.fn();
    const envelope = { code: 'KB_AUTH_FORBIDDEN', message: 'No permission.', hint: 'Ask an administrator.', details: { permission: 'auth:users:write' } };
    handleError({ ui: { error: output } }, Object.assign(new Error(envelope.message), { envelope }));
    expect(output).toHaveBeenCalledWith('KB_AUTH_FORBIDDEN: No permission. (permission: auth:users:write)', { hint: 'Ask an administrator.' });
  });
});
