/**
 * Version resolution helpers
 */

/**
 * Resolve CLI version from options or env
 */
export function resolveVersion(
  optionsVersion: string | undefined,
  env: NodeJS.ProcessEnv,
): string {
  const version = optionsVersion ?? env.CLI_VERSION ?? env.KB_VERSION;
  if (!version?.trim()) {
    throw new Error('CLI version metadata is required; rebuild the CLI package with its package.json version.');
  }
  return version;
}
