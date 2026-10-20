const PLATFORM_AUTH_EXEMPT_FLAGS = ['help', 'version'] as const;

export function isPlatformAuthExemptFlag(flag: 'help' | 'version'): boolean {
  return PLATFORM_AUTH_EXEMPT_FLAGS.includes(flag);
}
