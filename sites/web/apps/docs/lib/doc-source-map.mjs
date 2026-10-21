export const commandSources = Object.freeze({
  'workflow run': { source: 'cli/commands/src/commands/system/workflow.ts', selector: 'workflowRun' },
  'workflow runs list': { source: 'cli/commands/src/commands/system/workflow.ts', selector: 'runsList' },
  'workflow runs logs': { source: 'cli/commands/src/commands/system/workflow.ts', selector: 'runsLogs' },
  'workflow runs status': { source: 'cli/commands/src/commands/system/workflow.ts', selector: 'runsStatus' },
});

export const sourceFragmentSources = Object.freeze({
  nodePolicy: 'package.json',
});
