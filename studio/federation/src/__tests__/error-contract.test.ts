import { describe, expect, it, vi } from 'vitest';
import { normalizeStudioFailure, recordStudioFailure } from '../error-contract.js';

describe('Studio error boundary contract', () => {
  // case: S-203.5
  it('preserves typed KB envelope fields for a registered boundary', () => {
    const failure = normalizeStudioFailure({
      code: 'KB_AUTH_REQUIRED',
      message: 'Sign in to continue.',
      hint: 'Run kb auth login.',
      correlationId: 'corr-typed',
    });

    expect(failure).toEqual({
      code: 'KB_AUTH_REQUIRED',
      message: 'Sign in to continue.',
      hint: 'Run kb auth login.',
      correlationId: 'corr-typed',
      stack: undefined,
    });
  });

  it('rejects an uncatalogued KB code and preserves a valid product code from an API failure', () => {
    expect(normalizeStudioFailure({
      error: {
        code: 'KB_FAKE',
        message: 'Forged failure.',
        hint: 'Retry the request.',
        area: 'product',
      },
    })).toMatchObject({ code: 'KB_PLUGIN_CONTRACT_VIOLATION' });

    expect(normalizeStudioFailure({
      ok: false,
      error: {
        code: 'COMMIT_PUSH_REJECTED',
        area: 'product',
        stage: 'run',
        severity: 'error',
        retryable: false,
        message: 'The remote rejected the push.',
        hint: 'Fetch the latest changes, resolve conflicts, then retry.',
        details: { remote: 'origin' },
        correlationId: 'corr-product',
      },
    })).toMatchObject({
      code: 'COMMIT_PUSH_REJECTED',
      message: 'The remote rejected the push.',
      hint: 'Fetch the latest changes, resolve conflicts, then retry.',
      correlationId: 'corr-product',
    });
  });

  // case: S-203.5
  it('maps unknown failures to a correlated safe unexpected failure', () => {
    const failure = normalizeStudioFailure(new Error('private stack detail'));

    expect(failure.code).toBe('KB_RUNTIME_UNEXPECTED');
    expect(failure.message).toBe('The operation failed unexpectedly.');
    expect(failure.hint).toBe('Copy diagnostics and contact support.');
    expect(failure.correlationId).toMatch(/^studio-/);
    expect(failure.stack).toContain('private stack detail');
  });

  // case: S-203.5
  it('writes the stack only in a structured log carrying the displayed correlation ID', () => {
    const failure = normalizeStudioFailure(new Error('private stack detail'));
    const consoleDebug = vi.spyOn(console, 'debug').mockImplementation(() => {});
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
    const consoleWarn = vi.spyOn(console, 'warn').mockImplementation(() => {});

    recordStudioFailure('plugin page failed', failure);

    expect(consoleDebug).toHaveBeenCalledTimes(1);
    expect(consoleError).not.toHaveBeenCalled();
    expect(consoleWarn).not.toHaveBeenCalled();
    const record = JSON.parse(String(consoleDebug.mock.calls[0]?.[0])) as {
      level: string;
      code: string;
      correlationId: string;
      stack: string;
    };
    expect(record.level).toBe('error');
    expect(record.code).toBe(failure.code);
    expect(record.correlationId).toBe(failure.correlationId);
    expect(record.stack).toContain('private stack detail');
    expect(JSON.stringify(consoleError.mock.calls)).not.toContain('private stack detail');
    expect(JSON.stringify(consoleWarn.mock.calls)).not.toContain('private stack detail');
    consoleDebug.mockRestore();
    consoleError.mockRestore();
    consoleWarn.mockRestore();
  });
});
