/**
 * Match a permission pattern against a value.
 *
 * Permission patterns support exact values, trailing `*` prefix matches, and
 * `*` for every value.
 */
export function matchesPermissionPattern(value: string, pattern: string): boolean {
  if (pattern === '*') {
    return true;
  }
  if (pattern.endsWith('*')) {
    return value.startsWith(pattern.slice(0, -1));
  }
  return value === pattern;
}
