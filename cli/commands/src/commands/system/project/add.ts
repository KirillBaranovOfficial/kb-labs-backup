/** Register a project path in the machine-level project registry. */

import { defineSystemCommand, type CommandResult } from '@kb-labs/shared-command-kit';
import { getContextCwd } from '@kb-labs/shared-cli-ui';
import type { PluginContextV3 } from '@kb-labs/plugin-contracts';
import { addProject, projectErrorEnvelope, type AddProjectOutcome } from '@kb-labs/core-project-registry';
import type { ProjectRecord } from '@kb-labs/core-contracts';
import { generateExamples } from '../../../utils/generate-examples';
import { failWithEnvelope, openRegistry } from './project-support';

type Ui = PluginContextV3['ui'];

function reportPlanned(
  ui: Ui | undefined,
  outcome: Extract<AddProjectOutcome, { status: 'planned' }>,
  json: boolean,
) {
  const payload = {
    ok: true as const,
    dryRun: true,
    project: outcome.project,
  };
  if (json) {
    ui?.json(payload);
  } else {
    ui?.info(`Dry run: would register ${outcome.project.name} (${outcome.project.id}) at ${outcome.project.path}`);
  }
  return payload;
}

function reportAdded(ui: Ui | undefined, outcome: Extract<AddProjectOutcome, { status: 'added' }>, json: boolean) {
  const { project } = outcome;
  const payload = { ok: true as const, project };
  if (json) {
    ui?.json(payload);
  } else {
    ui?.success(`Registered ${project.name} (${project.id})`);
    ui?.write(`  path: ${project.path}\n`);
  }
  return payload;
}

type AddFlags = {
  json: { type: 'boolean'; description?: string };
  'dry-run': { type: 'boolean'; description?: string };
  name: { type: 'string'; description?: string };
};

export type ProjectAddResult = CommandResult & {
  dryRun?: boolean;
  project?: ProjectRecord;
};

export const projectAdd = defineSystemCommand<AddFlags, ProjectAddResult>({
  name: 'add',
  scope: 'platform',
  description: 'Register a project folder with the platform',
  longDescription:
    'Adds a folder to the machine-level project registry (KB_HOME/projects.json, default ~/.kb). ' +
    'Registration is based on the canonical path and does not read or write files in the project folder.',
  category: 'project',
  examples: generateExamples('project add', 'kb', [
    { flags: {}, description: 'register the current folder' },
    { flags: { 'dry-run': true, json: true } },
  ]),
  flags: {
    json: { type: 'boolean', description: 'Output machine-readable JSON' },
    'dry-run': { type: 'boolean', description: 'Show what would happen without changing anything' },
    name: { type: 'string', description: 'Alias for the project (default: folder name)' },
  },
  analytics: {
    command: 'project.add',
    startEvent: 'PROJECT_ADD_STARTED',
    finishEvent: 'PROJECT_ADD_FINISHED',
  },
  async handler(ctx, argv, flags) {
    const json = Boolean(flags.json);
    const dryRun = Boolean(flags['dry-run']);
    const ui = ctx.ui;

    try {
      const outcome = await addProject(openRegistry(), {
        path: argv[0] ?? '.',
        cwd: getContextCwd(ctx),
        name: flags.name,
        dryRun,
      });

      if (outcome.status === 'planned') {
        return reportPlanned(ui, outcome, json);
      }
      return reportAdded(ui, outcome, json);
    } catch (error) {
      return failWithEnvelope(ui, projectErrorEnvelope(error), json);
    }
  },
});
