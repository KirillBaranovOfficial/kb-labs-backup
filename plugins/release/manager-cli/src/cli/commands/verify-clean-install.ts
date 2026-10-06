/**
 * Standalone clean-install verification — installs a packed tarball into a
 * throwaway consumer project and confirms it can actually be imported.
 *
 * Exists as its own command so `check-pack-install.sh` (the bash release
 * gate) and `release stage` (the CI publish path) share ONE verification
 * implementation instead of the gate re-shelling to `npm install` on its
 * own and getting npm's swallowed, useless error message back (npm's CLI
 * catches the exact EUNSUPPORTEDPROTOCOL failure this exists to catch as an
 * unhandled rejection and prints nothing beyond "npm error, see log file").
 */

import { defineCommand, type CLIInput, type PluginCommandContextV3, type CommandResult } from '@kb-labs/sdk';
import { verifyCleanInstall, type CleanInstallResult } from '@kb-labs/release-manager-core';

interface VerifyCleanInstallFlags {
  tarball: string;
  name: string;
  registry?: string;
  json?: boolean;
}

export default defineCommand({
  id: 'release:clean-install',
  description: 'Install a packed tarball into a throwaway consumer and confirm it imports cleanly',

  handler: {
    async execute(ctx: PluginCommandContextV3, input: CLIInput<VerifyCleanInstallFlags>): Promise<CommandResult<CleanInstallResult>> {
      const { flags } = input;

      if (!flags.tarball || !flags.name) {
        const msg = 'release:clean-install requires --tarball <path> and --name <package-name>';
        return { ok: false, error: { code: 'KB_RUNTIME_INPUT_INVALID', message: msg, hint: 'Provide `--tarball <path>` and `--name <package-name>`, then retry.' } };
      }

      const result = await verifyCleanInstall(flags.tarball, flags.name, [], 'npm', flags.registry);

      if (!result.ok) {
        return { ok: false, error: { code: 'KB_RUNTIME_COMMAND_FAILED', message: result.error ?? 'Clean install verification failed', hint: 'Inspect the npm install failure, fix the tarball or registry issue, then retry.' } };
      }

      if (flags.json) {
        const response = { ok: true as const, result };
        return response;
      }

      ctx.ui?.write?.(`OK: ${flags.name} installs and imports cleanly`);
      return { ok: true, result };
    },
  },
});
