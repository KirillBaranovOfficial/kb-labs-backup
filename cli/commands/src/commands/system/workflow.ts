import {
  defineSystemCommand,
  defineSystemCommandGroup,
  type CommandGroup,
} from '@kb-labs/shared-command-kit';

const WORKFLOW_DAEMON_TEST_REASON = 'Requires an authenticated project gateway and a running workflow daemon.';
const workflowDaemonJsonTest = { noSafeValidPath: WORKFLOW_DAEMON_TEST_REASON } as const;
import {
  workflowCommands,
  defsListFlags,
  defsViewFlags,
  healthFlags,
  lintFlags,
  metricsFlags,
  runsApproveFlags,
  runsCancelFlags,
  runsListFlags,
  runsLogsFlags,
  runsRerunFlags,
  runsRestartFlags,
  runsStatusFlags,
  runsViewFlags,
  runsWatchFlags,
  runFlags,
  workflowRunFlags,
} from '@kb-labs/workflow-entry';

const create = defineSystemCommand({
  name: 'create', scope: 'project', jsonTest: { safeArgs: [] },
  description: 'Create a runnable local workflow example', category: 'workflow',
  flags: { json: { type: 'boolean', description: 'Output result as JSON' } },
  async handler(ctx, argv, flags) {
    return workflowCommands.create.execute(ctx, { argv, flags });
  },
});
const health = defineSystemCommand({
  name: 'health', scope: 'project', jsonTest: workflowDaemonJsonTest,
  description: 'Check workflow daemon health status', category: 'workflow', flags: healthFlags,
  async handler(ctx, argv, flags) {
    return workflowCommands.health.execute(ctx, { argv, flags });
  },
});
const metrics = defineSystemCommand({
  name: 'metrics', scope: 'project', jsonTest: workflowDaemonJsonTest,
  description: 'Get workflow daemon metrics', category: 'workflow', flags: metricsFlags,
  async handler(ctx, argv, flags) {
    return workflowCommands.metrics.execute(ctx, { argv, flags });
  },
});
const lint = defineSystemCommand({
  name: 'lint', scope: 'project', jsonTest: { safeArgs: ['--path', '__fixture_workflow__'] },
  description: 'Validate workflow files against the schema', category: 'workflow', flags: lintFlags,
  async handler(ctx, argv, flags) {
    return workflowCommands.lint.execute(ctx, { argv, flags });
  },
});
const workflowRun = defineSystemCommand({
  name: 'run', scope: 'project', jsonTest: workflowDaemonJsonTest,
  description: 'Run a workflow by workflow ID', category: 'workflow', flags: workflowRunFlags,
  examples: [
    'kb workflow run --workflow-id=release-manager/create-release',
    'kb workflow run --workflow-id=release-manager/create-release --isolation=strict --target-namespace=team-a/prod',
    'kb workflow run --workflow-id=release-manager/create-release --target-environment-id=env-123 --json',
    "kb workflow run --workflow-id=<workflow-id> --input='{\"key\":\"value\"}'",
  ],
  async handler(ctx, argv, flags) {
    return workflowCommands.workflowRun.execute(ctx, { argv, flags });
  },
});
const jobStatus = defineSystemCommand({
  name: 'status', scope: 'project', jsonTest: workflowDaemonJsonTest,
  description: 'Get raw job status', category: 'workflow',
  flags: { json: { type: 'boolean', description: 'Output result as JSON' } },
  async handler(ctx, argv, flags) {
    return workflowCommands.jobStatus.execute(ctx, { argv, flags });
  },
});
const jobList = defineSystemCommand({
  name: 'list', scope: 'project', jsonTest: workflowDaemonJsonTest,
  description: 'List active raw jobs', category: 'workflow',
  flags: { json: { type: 'boolean', description: 'Output result as JSON' }, status: { type: 'string', description: 'Filter by status' } },
  async handler(ctx, argv, flags) {
    return workflowCommands.jobList.execute(ctx, { argv, flags });
  },
});
const jobRun = defineSystemCommand({
  name: 'run', scope: 'project', jsonTest: workflowDaemonJsonTest,
  description: 'Submit a raw job for execution', category: 'workflow', flags: runFlags,
  async handler(ctx, argv, flags) {
    return workflowCommands.jobRun.execute(ctx, { argv, flags });
  },
});
const logShow = defineSystemCommand({
  name: 'show', scope: 'project', jsonTest: workflowDaemonJsonTest,
  description: 'Get raw job logs', category: 'workflow',
  flags: { json: { type: 'boolean', description: 'Output result as JSON' } },
  async handler(ctx, argv, flags) {
    return workflowCommands.logShow.execute(ctx, { argv, flags });
  },
});
const cronList = defineSystemCommand({
  name: 'list', scope: 'project', jsonTest: workflowDaemonJsonTest,
  description: 'List user and plugin workflow crons', category: 'workflow',
  flags: { json: { type: 'boolean', description: 'Output result as JSON' } },
  async handler(ctx, argv, flags) {
    return workflowCommands.cronsList.execute(ctx, { flags });
  },
});
const cronEnable = defineSystemCommand({
  name: 'enable', scope: 'project', jsonTest: workflowDaemonJsonTest,
  description: 'Enable a user workflow cron', category: 'workflow',
  flags: { json: { type: 'boolean', description: 'Output result as JSON' } },
  async handler(ctx, argv, flags) {
    return workflowCommands.cronsEnable.execute(ctx, { argv, flags });
  },
});
const cronDisable = defineSystemCommand({
  name: 'disable', scope: 'project', jsonTest: workflowDaemonJsonTest,
  description: 'Disable a user workflow cron', category: 'workflow',
  flags: { json: { type: 'boolean', description: 'Output result as JSON' } },
  async handler(ctx, argv, flags) {
    return workflowCommands.cronsDisable.execute(ctx, { argv, flags });
  },
});
const runsList = defineSystemCommand({
  name: 'list', scope: 'project', jsonTest: workflowDaemonJsonTest,
  description: 'List workflow runs', category: 'workflow', flags: runsListFlags,
  examples: [
    'kb workflow runs list',
    'kb workflow runs list --status=failed',
    'kb workflow runs list --status=failed --limit=5',
    'kb workflow runs list --workflow=my-workflow --json',
  ],
  async handler(ctx, argv, flags) {
    return workflowCommands.runsList.execute(ctx, { argv, flags });
  },
});
const runsView = defineSystemCommand({
  name: 'view', scope: 'project', jsonTest: workflowDaemonJsonTest,
  description: 'View a workflow run', category: 'workflow', flags: runsViewFlags,
  async handler(ctx, argv, flags) {
    return workflowCommands.runsView.execute(ctx, { argv, flags });
  },
});
const runsWatch = defineSystemCommand({
  name: 'watch', scope: 'project', jsonTest: workflowDaemonJsonTest,
  description: 'Watch a workflow run', category: 'workflow', flags: runsWatchFlags,
  async handler(ctx, argv, flags) {
    return workflowCommands.runsWatch.execute(ctx, { argv, flags });
  },
});
const runsRerun = defineSystemCommand({
  name: 'rerun', scope: 'project', jsonTest: workflowDaemonJsonTest,
  description: 'Rerun a workflow run', category: 'workflow', flags: runsRerunFlags,
  async handler(ctx, argv, flags) {
    return workflowCommands.runsRerun.execute(ctx, { argv, flags });
  },
});
const runsRestart = defineSystemCommand({
  name: 'restart', scope: 'project', jsonTest: workflowDaemonJsonTest,
  description: 'Restart a workflow run from a step', category: 'workflow', flags: runsRestartFlags,
  async handler(ctx, argv, flags) {
    return workflowCommands.runsRestart.execute(ctx, { argv, flags });
  },
});
const runsCancel = defineSystemCommand({
  name: 'cancel', scope: 'project', jsonTest: workflowDaemonJsonTest,
  description: 'Cancel a workflow run', category: 'workflow', flags: runsCancelFlags,
  async handler(ctx, argv, flags) {
    return workflowCommands.runsCancel.execute(ctx, { argv, flags });
  },
});
const runsLogs = defineSystemCommand({
  name: 'logs', scope: 'project', jsonTest: workflowDaemonJsonTest,
  description: 'Show workflow run logs', category: 'workflow', flags: runsLogsFlags,
  examples: [
    'kb workflow runs logs <runId>',
    'kb workflow runs logs <runId> --log-failed',
    'kb workflow runs logs <runId> --step=build',
    'kb workflow runs logs <runId> --json',
  ],
  async handler(ctx, argv, flags) {
    return workflowCommands.runsLogs.execute(ctx, { argv, flags });
  },
});
const runsStatus = defineSystemCommand({
  name: 'status', scope: 'project', jsonTest: workflowDaemonJsonTest,
  description: 'Get workflow run status', category: 'workflow', flags: runsStatusFlags,
  examples: [
    'kb workflow runs status <runId>',
    'kb workflow runs status --run-id=<runId>',
    'kb workflow runs status <runId> --json',
  ],
  async handler(ctx, argv, flags) {
    return workflowCommands.runsStatus.execute(ctx, { argv, flags });
  },
});
const runsApprove = defineSystemCommand({
  name: 'approve', scope: 'project', jsonTest: workflowDaemonJsonTest,
  description: 'Approve or reject a pending workflow step', category: 'workflow', flags: runsApproveFlags,
  async handler(ctx, argv, flags) {
    return workflowCommands.runsApprove.execute(ctx, { argv, flags });
  },
});
const defsList = defineSystemCommand({
  name: 'list', scope: 'project', jsonTest: workflowDaemonJsonTest,
  description: 'List workflow definitions', category: 'workflow', flags: defsListFlags,
  async handler(ctx, argv, flags) {
    return workflowCommands.defsList.execute(ctx, { argv, flags });
  },
});
const defsView = defineSystemCommand({
  name: 'view', scope: 'project', jsonTest: workflowDaemonJsonTest,
  description: 'View a workflow definition', category: 'workflow', flags: defsViewFlags,
  async handler(ctx, argv, flags) {
    return workflowCommands.defsView.execute(ctx, { argv, flags });
  },
});

const jobGroup: CommandGroup = {
  ...defineSystemCommandGroup('job', 'Raw workflow job commands', [jobStatus, jobList, jobRun]),
};
const logGroup: CommandGroup = defineSystemCommandGroup('log', 'Workflow job log commands', [logShow]);
const cronGroup: CommandGroup = defineSystemCommandGroup('crons', 'Workflow schedule management', [cronList, cronEnable, cronDisable]);
const runsGroup: CommandGroup = defineSystemCommandGroup('runs', 'Workflow run management', [
  runsList, runsView, runsWatch, runsRerun, runsRestart, runsCancel, runsLogs, runsStatus, runsApprove,
]);
const defsGroup: CommandGroup = defineSystemCommandGroup('defs', 'Workflow definition discovery', [defsList, defsView]);

export const workflowGroup: CommandGroup = {
  ...defineSystemCommandGroup('workflow', 'Workflow daemon commands', [create, health, metrics, lint, workflowRun]),
  subgroups: [jobGroup, logGroup, cronGroup, runsGroup, defsGroup],
};
