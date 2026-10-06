import { defineCommand, handleError, validationError, type CLIInput, type PluginCommandContextV3 } from '@kb-labs/sdk';
import { updateProject } from '@kb-labs/steward-core';

type Flags = { status?: string; description?: string; json?: boolean };

export default defineCommand({
  id: 'steward:project.update',
  description: "Update a project's status or description",

  handler: {
    async execute(ctx: PluginCommandContextV3, input: CLIInput<Flags>) {
      const [id] = input.argv;
      const { status, description, json } = input.flags;
      if (!id) {
        return validationError(ctx, 'id is required', 'Usage: kb steward project update <id> [--status] [--description]', json);
      }

      try {
        const project = await updateProject({
          id,
          status: status as 'active' | 'paused' | 'archived' | undefined,
          description,
        });
        if (!project) {
          return { ok: false, error: { code: 'KB_RUNTIME_RESOURCE_NOT_FOUND', message: `No project "${id}"`, hint: "Run `kb steward project update --help` to review the failure and next steps." }, result: null };
        }

        if (json) {return { ok: true, result: project };}
        else {ctx.ui?.info?.(`Updated "${project.name}" → [${project.status}]`);}
        return { ok: true, result: project };
      } catch (err) {
        ctx.platform.logger?.error?.('steward:project.update failed', err instanceof Error ? err : new Error(String(err)));
        return handleError(ctx, err, json);
      }
    },
  },
});
