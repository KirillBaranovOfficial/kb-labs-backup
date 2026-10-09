import { describe, expect, it } from 'vitest';
import { createErrorEnvelope } from '../error-envelope/catalog.js';
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

  it.each([
    [401, 'KB_AUTH_REQUIRED'],
    [403, 'KB_AUTH_FORBIDDEN'],
    [400, 'KB_RUNTIME_INPUT_INVALID'],
    [502, 'KB_RUNTIME_UNEXPECTED'],
  ])('derives a catalog fallback code for an uncatalogued HTTP %i failure', (statusCode, code) => {
    expect(codeForHttpFailure(new Error('upstream'), statusCode)).toBe(code);
  });
});
