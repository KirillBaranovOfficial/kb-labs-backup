/**
 * Sandboxed environment variable access
 */

import { matchesPermissionPattern } from '@kb-labs/plugin-contracts';
import type { PermissionSpec, EnvShim } from '@kb-labs/plugin-contracts';

/**
 * Environment variables that are always allowed (safe)
 */
const ALWAYS_ALLOWED = [
  'NODE_ENV',
  'CI',
  'DEBUG',
  'TZ',
  'LANG',
  'LC_ALL',
];

export interface CreateEnvShimOptions {
  permissions: PermissionSpec;
  environment?: Record<string, string | undefined>;
}

/**
 * Create a sandboxed env access function
 *
 * Non-whitelisted vars return undefined (no error thrown).
 */
export function createEnvShim(options: CreateEnvShimOptions): EnvShim {
  const { permissions, environment } = options;
  const allowedPatterns = permissions.env?.read ?? [];

  return (key: string): string | undefined => {
    // Check always allowed
    if (ALWAYS_ALLOWED.includes(key)) {
      return environment?.[key] ?? process.env[key];
    }

    // Check whitelist patterns
    const isAllowed = allowedPatterns.some(pattern => matchesPermissionPattern(key, pattern));

    if (!isAllowed) {
      // Return undefined for non-allowed vars (no error)
      return undefined;
    }

    return environment?.[key] ?? process.env[key];
  };
}
