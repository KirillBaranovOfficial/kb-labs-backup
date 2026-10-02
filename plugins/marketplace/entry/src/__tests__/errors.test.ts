import { describe, expect, it, vi } from 'vitest';
import { handleError } from '@kb-labs/sdk';

describe('marketplace CLI errors', () => {
  // case: S-041.7
  it('renders missing authentication as a typed error envelope', () => {
    const json = vi.fn();
    const ctx = { ui: { json } };

    handleError(ctx, {
      envelope: {
        code: 'KB_AUTH_REQUIRED', area: 'auth', stage: 'run', severity: 'error', retryable: false,
        message: 'Authentication is required.', hint: 'Run "kb auth login".',
      },
    }, true);

    expect(json).toHaveBeenCalledWith({
      ok: false,
      error: {
        code: 'KB_AUTH_REQUIRED',
        area: 'auth',
        stage: 'run',
        severity: 'error',
        retryable: false,
        message: 'Authentication is required.',
        hint: 'Run "kb auth login".',
      },
    });
  });
});
