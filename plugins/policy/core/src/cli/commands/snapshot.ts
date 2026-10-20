import { defineCommand, findRepoRoot, type PluginCommandContextV3, type CommandResult } from '@kb-labs/sdk';
import { updateSnapshots } from '../../checks/api-compat-check.js';

type SnapshotInput = {
  path: string;
};

export default defineCommand({
  id: 'policy:snapshot',
  description: 'Create or update the API snapshot for packages in a repo. Run after npm publish.',

  handler: {
    async intent(_ctx: PluginCommandContextV3, input: SnapshotInput) {
      const flags = (input as { flags?: SnapshotInput }).flags ?? input;
      const path = flags.path ?? '(unknown)';
      return {
        summary: `Create or update API snapshot for "${path}"`,
        operations: [
          { type: 'create' as const, resource: 'file', details: { path: `.kb/api-snapshots/${path}` } },
        ],
      };
    },

    async execute(ctx: PluginCommandContextV3, input: SnapshotInput): Promise<CommandResult> {
      const flags = (input as { flags?: SnapshotInput }).flags ?? input;

      if (!flags.path) {
        return { ok: false, error: { code: 'KB_RUNTIME_INPUT_INVALID', message: '--path is required.', hint: 'Run `kb policy snapshot --help` to see the required --path argument.' } };
      }

      const workspaceRoot = (await findRepoRoot(ctx.cwd)) ?? ctx.cwd;

      ctx.ui.info?.(`Extracting API snapshot for ${flags.path}...`);
      updateSnapshots(flags.path, workspaceRoot);
      ctx.ui.success?.('Snapshot updated', {
        sections: [
          {
            header: 'Info',
            items: [
              `Path: ${flags.path}`,
              `Snapshots saved to: .kb/api-snapshots/`,
            ],
          },
        ],
      });

      return {};
    },
  },
});
