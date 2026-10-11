import { describe, expect, it } from 'vitest';
import { createErrorEnvelope, createProductErrorEnvelope, toErrorEnvelope } from '../error-envelope/index.js';
import { createUxFailureResult } from '../ux-result/index.js';
import { codeForHttpFailure, normalizeHttpFailure, statusForHttpFailure } from './index.js';

describe('shared HTTP failure boundary', () => {
  it('preserves catalogued error details and derives its status from the error catalog', () => {
    const error = createErrorEnvelope('KB_PROJECT_UNKNOWN', {
      message: 'Project missing',
      hint: 'Register the project first.',
      details: { projectId: 'project-1' },
    });

    expect(statusForHttpFailure({ error }, 500)).toBe(404);
    expect(codeForHttpFailure({ error }, 500)).toBe('KB_PROJECT_UNKNOWN');
    expect(normalizeHttpFailure({ error }, { correlationId: 'request-1', statusCode: 500 })).toEqual({
      ok: false,
      error: { ...error, correlationId: 'request-1' },
    });
  });

  it('normalizes a plain known code and rejects a catalog code without an HTTP status', () => {
    expect(normalizeHttpFailure({ error: { code: 'KB_RUNTIME_INPUT_INVALID' } }, { correlationId: 'request-2', statusCode: 500 })).toMatchObject({
      ok: false,
      error: { code: 'KB_RUNTIME_INPUT_INVALID', correlationId: 'request-2' },
    });
    expect(normalizeHttpFailure(new Error('private'), { correlationId: 'request-3', statusCode: 500 }).error).toMatchObject({
      code: 'KB_RUNTIME_UNEXPECTED', correlationId: 'request-3',
    });

    expect(statusForHttpFailure(new Error('upstream'), 502)).toBe(502);
    expect(() => statusForHttpFailure({ code: 'KB_AUTH_CLOCK_SKEW' }, 401)).toThrow(
      'Catalogued error "KB_AUTH_CLOCK_SKEW" reached an HTTP boundary without httpStatus',
    );
  });

  it('preserves a validated product failure and keeps the transport HTTP status', () => {
    const failure = createUxFailureResult(createProductErrorEnvelope({
      code: 'COMMIT_PUSH_REJECTED',
      message: 'The remote rejected the push.',
      hint: 'Fetch the latest changes, resolve conflicts, then retry.',
      retryable: false,
    }));

    expect(statusForHttpFailure(failure, 409)).toBe(409);
    expect(normalizeHttpFailure(failure, { correlationId: 'request-product', statusCode: 409 })).toMatchObject({
      ok: false,
      error: {
        code: 'COMMIT_PUSH_REJECTED',
        area: 'product',
        message: 'The remote rejected the push.',
        hint: 'Fetch the latest changes, resolve conflicts, then retry.',
        correlationId: 'request-product',
      },
    });
  });

  it.each([
    {
      code: 'KB_FAKE',
      area: 'product',
      stage: 'run',
      severity: 'error',
      retryable: false,
      message: 'Forged platform error.',
      hint: 'Retry the request.',
    },
    {
      code: 'COMMIT_INVALID_DETAILS',
      area: 'product',
      stage: 'run',
      severity: 'error',
      retryable: false,
      message: 'Product error with serialized details.',
      hint: 'Keep details flat.',
      details: { report: '{"invalid":"nested"}' },
    },
  ])('validates a failure-shaped HTTP payload before preserving it', (error) => {
    const result = normalizeHttpFailure(
      { ok: false, error },
      { correlationId: 'request-untrusted', statusCode: 422 },
    );

    expect(result.error.code).toBe('KB_PLUGIN_CONTRACT_VIOLATION');
    expect(result.error.message).not.toBe(error.message);
    expect(statusForHttpFailure({ ok: false, error }, 422)).toBe(422);
  });

  it.each([
    [401, 'KB_AUTH_REQUIRED'],
    [403, 'KB_AUTH_FORBIDDEN'],
    [400, 'KB_RUNTIME_INPUT_INVALID'],
    [404, 'KB_RUNTIME_UNEXPECTED'],
    [502, 'KB_RUNTIME_UNEXPECTED'],
  ])('derives a catalog fallback code for an uncatalogued HTTP %i failure', (statusCode, code) => {
    expect(codeForHttpFailure(new Error('upstream'), statusCode)).toBe(code);
  });

  it('maps framework validation and operating-system errors without treating their codes as product codes', () => {
    expect(toErrorEnvelope(Object.assign(new Error('invalid request'), {
      code: 'FST_ERR_VALIDATION',
      statusCode: 400,
      validation: [{ keyword: 'required' }],
    })).code).toBe('KB_RUNTIME_INPUT_INVALID');
    expect(toErrorEnvelope(Object.assign(new Error('connect failed'), {
      code: 'ECONNREFUSED',
      errno: -61,
      syscall: 'connect',
    })).code).toBe('KB_RUNTIME_UNEXPECTED');
  });
});
