/** Platform-owned commands that must work before a user can sign in. */
const PLATFORM_AUTH_EXEMPT_COMMANDS = [
  'auth login',
  'auth logout',
  'auth status',
  'auth register',
  'info version',
  'info health',
  'diag',
];

const PLATFORM_AUTH_EXEMPT_FLAGS = ['help', 'version'] as const;

export function isPlatformAuthExemptFlag(flag: 'help' | 'version'): boolean {
  return PLATFORM_AUTH_EXEMPT_FLAGS.includes(flag);
}

export function isPlatformAuthExempt(commandPath: string, commandKind: 'system' | 'plugin'): boolean {
  return commandKind === 'system' && PLATFORM_AUTH_EXEMPT_COMMANDS.includes(commandPath);
}
