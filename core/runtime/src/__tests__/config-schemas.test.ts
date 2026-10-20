import { describe, it, expect } from 'vitest';
import { validateExecutionConfig } from '../config-schemas.js';

// ── Execution mode validation ────────────────────────────────────────────────

describe('Container execution config', () => {
  it('accepts container mode without a Gateway configuration object', () => {
    expect(validateExecutionConfig({ mode: 'container' })).toMatchObject({ mode: 'container' });
  });

  it('rejects the removed Gateway container configuration instead of ignoring it', () => {
    expect(() => validateExecutionConfig({
      mode: 'container',
      container: { gatewayInternalSecret: 'obsolete' },
    })).toThrow(/Unrecognized key/);
  });
});

// ── ExecutionConfig mode validation ──────────────────────────────────────────

describe('ExecutionConfig mode', () => {
  it('rejects a workspace-agent Gateway URL in config', () => {
    expect(() => validateExecutionConfig({
      workspaceAgent: {
        enabled: true,
        gatewayUrl: 'http://localhost:4000',
        internalSecret: 'secret',
      },
    })).toThrow(/gatewayUrl/);
  });

  it('accepts workspace-agent routing without a configured Gateway URL', () => {
    const result = validateExecutionConfig({
      workspaceAgent: { enabled: true, internalSecret: 'secret', fallback: 'error' },
    });
    expect(result.workspaceAgent).toEqual({
      enabled: true,
      internalSecret: 'secret',
      fallback: 'error',
    });
  });

  it('accepts mode: in-process', () => {
    const result = validateExecutionConfig({ mode: 'in-process' });
    expect(result.mode).toBe('in-process');
  });

  it('accepts mode: worker-pool with workerPool config', () => {
    const result = validateExecutionConfig({
      mode: 'worker-pool',
      workerPool: { min: 2, max: 10, maxRequestsPerWorker: 500 },
    });
    expect(result.workerPool?.min).toBe(2);
    expect(result.workerPool?.max).toBe(10);
  });

  it('accepts mode: auto (no extra required fields)', () => {
    const result = validateExecutionConfig({ mode: 'auto' });
    expect(result.mode).toBe('auto');
  });

  it('accepts empty config (all optional)', () => {
    const result = validateExecutionConfig({});
    expect(result.mode).toBeUndefined();
  });

  it('rejects unknown mode', () => {
    expect(() => validateExecutionConfig({ mode: 'kubernetes' as 'auto' })).toThrow();
  });
});

// ── superRefine cross-field rules ─────────────────────────────────────────────

describe('ExecutionConfig cross-field rules', () => {
  it('rejects remote mode during config validation', () => {
    expect(() => validateExecutionConfig({ mode: 'remote' })).toThrow(
      /remote execution is not supported in this release/,
    );
  });
});

// ── validateExecutionConfig error formatting ──────────────────────────────────

describe('validateExecutionConfig error message', () => {
  it('includes all issue paths in the error message', () => {
    let err: Error | undefined;
    try {
      validateExecutionConfig({ mode: 'container', unexpected: true });
    } catch (e) {
      err = e as Error;
    }
    expect(err).toBeDefined();
    expect(err!.message).toContain('Invalid execution config');
    expect(err!.message).toMatch(/unexpected/);
  });

  it('lists multiple issues when there are multiple errors', () => {
    let err: Error | undefined;
    try {
      validateExecutionConfig({ mode: 'container', unexpected: true, another: true });
    } catch (e) {
      err = e as Error;
    }
    expect(err!.message).toContain('•');
  });
});

// ── ExecutionRetryConfig ──────────────────────────────────────────────────────

describe('ExecutionRetryConfig', () => {
  it('accepts valid retry config', () => {
    const result = validateExecutionConfig({
      retry: { maxAttempts: 3, initialDelayMs: 500, backoffMultiplier: 2, maxDelayMs: 10000, onlyRetryable: true },
    });
    expect(result.retry?.maxAttempts).toBe(3);
  });

  it('rejects maxAttempts < 1', () => {
    expect(() => validateExecutionConfig({ retry: { maxAttempts: 0 } })).toThrow();
  });

  it('rejects negative initialDelayMs', () => {
    expect(() => validateExecutionConfig({ retry: { initialDelayMs: -1 } })).toThrow();
  });
});
