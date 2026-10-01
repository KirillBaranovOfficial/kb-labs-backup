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

  it('keeps untyped errors in the internal error envelope', () => {
    const json = vi.fn();
    handleError({ ui: { json } }, new Error('request failed'), true);
    expect(json).toHaveBeenCalledWith({
      ok: false,
      error: { code: 'INTERNAL_ERROR', message: 'request failed' },
    });
  });
});
