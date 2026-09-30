import { describe, expect, it } from 'vitest';
import { isFeatureEnabled, areDependenciesMet } from '../feature-flags.js';

describe('feature preferences', () => {
  it('accepts a partial preference map and falls back to feature defaults', () => {
    expect(isFeatureEnabled('settings-system', {})).toBe(true);
    expect(isFeatureEnabled('settings-system', { 'devtools-panel': true })).toBe(true);
    expect(isFeatureEnabled('devtools-panel', { 'devtools-panel': false })).toBe(false);
    expect(areDependenciesMet('settings-system', {})).toBe(true);
  });
});
