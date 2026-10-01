import { describe, expect, it, vi } from 'vitest';
import { handleError } from '../utils/errors.js';

class TypedAuthenticationError extends Error {
  readonly code = 'KB_AUTH_REQUIRED';
  readonly hint = 'Run "kb auth login".';
}

describe('handleError', () => {
  it('preserves typed error codes and hints in JSON and human output', () => {
    const json = vi.fn();
    const output = vi.fn();

    handleError({ ui: { json, error: output } }, new TypedAuthenticationError('Workflow access requires a login.'), true);

    expect(json).toHaveBeenCalledWith({
      ok: false,
      error: {
        code: 'KB_AUTH_REQUIRED',
        message: 'Workflow access requires a login.',
        hint: 'Run "kb auth login".',
      },
    });

    handleError({ ui: { json, error: output } }, new TypedAuthenticationError('Workflow access requires a login.'));
    expect(output).toHaveBeenCalledWith('Workflow access requires a login.', {
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
