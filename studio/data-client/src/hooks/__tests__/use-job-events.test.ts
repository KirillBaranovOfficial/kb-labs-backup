import { describe, expect, it } from 'vitest';
import { decodeJobResponseData } from '../use-job-events';

describe('decodeJobResponseData', () => {
  // case: S-203.1
  it('unwraps a valid Gateway result envelope', () => {
    expect(decodeJobResponseData({ ok: true, data: { jobId: 'job-1', status: 'running', progress: 25 } }))
      .toEqual({ jobId: 'job-1', status: 'running', progress: 25 });
  });

  // case: S-203.2
  it('surfaces a safe error from a failed Gateway result envelope', () => {
    expect(() => decodeJobResponseData({
        ok: false,
        error: {
          code: 'KB_RUNTIME_UNEXPECTED', area: 'runtime', stage: 'run', severity: 'error', retryable: false,
          message: 'The operation failed unexpectedly.', hint: 'Copy diagnostics and contact support.', correlationId: 'corr-1',
        },
    })).toThrow('KB_RUNTIME_UNEXPECTED: The operation failed unexpectedly. — Copy diagnostics and contact support. (correlationId: corr-1)');
  });

  it('canonicalizes an invalid failure code before surfacing it', () => {
    expect(() => decodeJobResponseData({
        ok: false,
        error: {
          code: 'KB_FAKE', area: 'product', stage: 'run', severity: 'error', retryable: false,
          message: 'forged', hint: 'forged',
        },
    })).toThrow(/^KB_PLUGIN_CONTRACT_VIOLATION:/);
  });

  it('rejects raw payloads and invalid job data', () => {
    expect(() => decodeJobResponseData({ jobId: 'job-1', status: 'running' })).toThrow('invalid result envelope');
    expect(() => decodeJobResponseData({ ok: true, data: { jobId: 'job-1' } })).toThrow('invalid job response payload');
  });
});
