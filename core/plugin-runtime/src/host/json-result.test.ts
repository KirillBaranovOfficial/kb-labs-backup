import { describe, expect, it } from 'vitest';
import { normalizeJsonResult } from './json-result.js';

describe('normalizeJsonResult', () => {
  // case: S-203.1
  it('preserves an ok field that belongs to the command payload', () => {
    expect(normalizeJsonResult({ ok: false, report: 'degraded' })).toEqual({
      ok: true,
      data: { ok: false, report: 'degraded' },
    });
  });
});
