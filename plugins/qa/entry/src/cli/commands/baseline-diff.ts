import {
  defineCommand,
  useConfig,
  type CLIInput,
  type PluginCommandContextV3,
  type CommandResult,
} from '@kb-labs/sdk';
import {
  SnapshotStore,
  compareWithBaseline,
  buildBaselineDiffReport,
} from '@kb-labs/qa-core';
import { type QAPluginConfig } from '@kb-labs/qa-contracts';
import type { BaselineDiffFlags } from './flags.js';

export default defineCommand<unknown, CLIInput<BaselineDiffFlags>, unknown>({
  id: 'qa:baseline:diff',
  description: 'Diff current state against baseline',

  handler: {
    async execute(ctx: PluginCommandContextV3, input: CLIInput<BaselineDiffFlags>): Promise<CommandResult> {
      const { flags } = input;
      const config = await useConfig<QAPluginConfig>();
      const cwd = ctx.cwd ?? process.cwd();

      const store = new SnapshotStore(cwd, config?.historyMaxEntries);
      const baseline = store.loadBaseline();

      if (!baseline) {

        return { ok: false, error: { code: 'KB_RUNTIME_INPUT_INVALID', message: 'No QA baseline was found.', hint: 'Run `kb qa baseline update`, then rerun `kb qa baseline diff`.' } };
      }

      const latestCheck = store.latestCheck();
      const latestStats = store.latestStats();

      if (!latestCheck || !latestStats) {

        return { ok: false, error: { code: 'KB_RUNTIME_INPUT_INVALID', message: 'No QA check or stats snapshots were found.', hint: 'Run `kb qa check` and `kb qa stats`, then rerun `kb qa baseline diff`.' } };
      }

      const diff = compareWithBaseline(latestCheck.raw, latestStats.raw, baseline);

      if (flags.json) {
        return { ok: true, result: diff };
      } else {
        for (const section of buildBaselineDiffReport(diff)) {
          ctx.ui?.success?.(`${section.header}`, { sections: [{ items: section.lines }] });
        }
      }

      return diff.newIssueCount > 0
        ? { ok: false, error: { code: 'KB_RUNTIME_COMMAND_FAILED', message: 'New issues found', hint: 'Run `kb qa --help` to review the command inputs, then rerun the command.' }, result: diff }
        : { ok: true, result: diff };
    },
  },
});
