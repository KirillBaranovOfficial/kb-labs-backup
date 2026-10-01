/** Platform-owned commands that must work before a user can sign in. */
export const PLATFORM_AUTH_EXEMPT_COMMANDS = new Set([
  'auth login',
  'auth logout',
  'auth status',
  'auth register',
  'info version',
  'info health',
  'diag',
]);

export const PLATFORM_AUTH_EXEMPT_FLAGS = new Set(['help', 'version']);

export function isPlatformAuthExemptFlag(flag: 'help' | 'version'): boolean {
  return PLATFORM_AUTH_EXEMPT_FLAGS.has(flag);
}

export function isPlatformAuthExempt(commandPath: string, commandKind: 'system' | 'plugin'): boolean {
  return commandKind === 'system' && PLATFORM_AUTH_EXEMPT_COMMANDS.has(commandPath);
}
