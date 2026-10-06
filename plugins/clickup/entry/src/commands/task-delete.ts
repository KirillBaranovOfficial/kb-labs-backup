import { defineCommand, type CLIInput, type PluginCommandContextV3 } from '@kb-labs/sdk';
import { requireApiKey, deleteTask } from '@kb-labs/clickup-core';
import { handleError, validationError } from '../utils/error.js';

type TaskDeleteFlags = { yes?: boolean; json?: boolean; 'dry-run'?: boolean };

export default defineCommand({
  id: 'clickup:task.delete',
  description: 'Delete a task',

  handler: {
    async intent(_ctx: PluginCommandContextV3, input: CLIInput<TaskDeleteFlags>) {
      const [taskId] = input.argv;
      return {
        summary: `Delete task ${taskId ?? '(unknown)'}`,
        operations: [{ type: 'delete' as const, resource: 'task', details: { taskId } }],
      };
    },

    async execute(ctx: PluginCommandContextV3, input: CLIInput<TaskDeleteFlags>) {
      const [taskId] = input.argv;
      if (!taskId) {
        return validationError(ctx, 'Task ID is required', 'Usage: kb clickup task delete <taskId> --yes', input.flags.json);
      }

      if (!input.flags.yes) {
        return validationError(ctx, `--yes is required to delete task ${taskId}`, 'Add --yes to confirm deletion', input.flags.json);
      }

      try {
        await deleteTask(requireApiKey(), taskId);

        if (input.flags.json) {
          return { ok: true, result: { deleted: true, taskId } };
        } else {
          ctx.ui?.success?.(`Deleted task ${taskId}`);
        }

        return { ok: true, result: { deleted: true, taskId } };
      } catch (err) {
        return handleError(ctx, err, input.flags.json);
      }
    },
  },
});
