import { describe, expect, it } from 'vitest';
import { resolveColorSupport } from '../color-policy.js';

describe('CLI color policy', () => {
  it('keeps piped non-TTY output plain unless FORCE_COLOR is explicit', () => {
    expect(resolveColorSupport(false, undefined, undefined)).toBe(false);
    expect(resolveColorSupport(false, undefined, '1')).toBe(true);
  });

  it('lets NO_COLOR disable colors and enables a TTY by default', () => {
    expect(resolveColorSupport(true, undefined, undefined)).toBe(true);
    expect(resolveColorSupport(true, '1', '1')).toBe(false);
  });
});
