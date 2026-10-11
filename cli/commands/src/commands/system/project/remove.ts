/**
 * project remove — unregister a project.
 *
 * Only the registry entry goes away: the project folder, its `.kb/` and its
 * runtime state directory are never touched. Follows the destructive-action
 * protocol (ADR-0025): without --yes nothing happens, in human and --json mode.
 */

import { defineSystemCommand, throwSystemCataloguedError, type CommandResult } from '@kb-labs/shared-command-kit';
import { toErrorEnvelope } from '@kb-labs/core-platform';
import { confirmDestructive, getContextCwd } from '@kb-labs/shared-cli-ui';
import type { ProjectRecord } from '@kb-labs/core-contracts';
import { projectErrorEnvelope } from '@kb-labs/core-project-registry';
import { generateExamples } from '../../../utils/generate-examples';
import { failWithEnvelope, openRegistry, projectRef } from './project-support';

type RemoveFlags = {
  json: { type: 'boolean'; description?: string };
  'dry-run': { type: 'boolean'; description?: string };
  yes: { type: 'boolean'; description?: string };
};

export type ProjectRemoveResult = CommandResult & {
  dryRun?: boolean;
  project?: ProjectRecord;
};

export const projectRemove = defineSystemCommand<RemoveFlags, ProjectRemoveResult>({
  name: 'remove',
  scope: 'platform',
  jsonTest: { noSafeValidPath: "This command changes the machine project registry." },
  description: 'Unregister a project (files are not deleted)',
  longDescription:
    'Removes a project from the registry by id, name or path. The folder, its .kb/ and its runtime ' +
    'state are left untouched; registering the same folder again restores the same project id.',
  category: 'project',
  examples: generateExamples('project remove', 'kb', [
    { flags: { 'dry-run': true } },
    { flags: { yes: true } },
  ]),
  flags: {
    json: { type: 'boolean', description: 'Output machine-readable JSON' },
    'dry-run': { type: 'boolean', description: 'Show what would be removed without changing anything' },
    yes: { type: 'boolean', description: 'Do not ask for confirmation' },
  },
  analytics: {
    command: 'project.remove',
    startEvent: 'PROJECT_REMOVE_STARTED',
    finishEvent: 'PROJECT_REMOVE_FINISHED',
  },
  async handler(ctx, argv, flags) {
    const json = Boolean(flags.json);
    try {
      const registry = openRegistry();
      const ref = projectRef(argv[0], getContextCwd(ctx));
      // Resolve first so an unknown project reports KB_PROJECT_UNKNOWN before any prompt.
      const { project } = await registry.get(ref);

      if (flags['dry-run']) {
        if (!json) {
          ctx.ui?.info(`Dry run: would unregister ${project.name} (${project.id}); no files are deleted.`);
        }
        return {  dryRun: true, project };
      }

      const blocked = confirmDestructive({
        confirmed: Boolean(flags.yes),
        action: {
          action: 'project remove',
          resource: `project "${project.name}" (${project.path})`,
          effect: 'unregisters the project from the platform; files and runtime state are kept',
          severity: 'low',
          reversible: true,
          recovery: `re-register with \`kb project add ${project.path}\``,
          blastRadius: { count: 1, unit: 'registry entry' },
        },
      });
      if (blocked) {
        throwSystemCataloguedError(toErrorEnvelope(blocked.error));
      }

      const removed = await registry.remove(project.id);
      if (!json) {
        ctx.ui?.success(`Unregistered ${removed.name} (${removed.id}); files were not touched.`);
      }
      return {  project: removed };
    } catch (error) {
      if (error instanceof Error && 'code' in error && 'hint' in error) {
        throw error;
      }
      return failWithEnvelope(projectErrorEnvelope(error));
    }
  },
});
