import { describe, expect, it } from 'vitest';
import { matchesPermissionPattern } from './permission-pattern.js';

describe('permission patterns', () => {
  it('matches exact values', () => {
    expect(matchesPermissionPattern('KB_GATEWAY_URL', 'KB_GATEWAY_URL')).toBe(true);
    expect(matchesPermissionPattern('KB_GATEWAY_URL', 'KB_GATEWAY_HOST')).toBe(false);
  });

  it('matches values with a trailing prefix wildcard', () => {
    expect(matchesPermissionPattern('KB_GATEWAY_URL', 'KB_GATEWAY_*')).toBe(true);
    expect(matchesPermissionPattern('GATEWAY_URL', 'KB_GATEWAY_*')).toBe(false);
  });

  it('matches every value with the global wildcard', () => {
    expect(matchesPermissionPattern('anything', '*')).toBe(true);
  });
});
