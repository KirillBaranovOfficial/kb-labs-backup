import {
  defineCommand,
  useConfig,
  type CLIInput,
  type PluginCommandContextV3,
  type CommandResult,
} from '@kb-labs/sdk';
import { runKnip } from '@kb-labs/quality-core';
import { defaultQualityConfig, type QualityPluginConfig } from '@kb-labs/quality-contracts';
import type { DeadCodeFlags } from './flags.js';

export default defineCommand<unknown, CLIInput<DeadCodeFlags>, unknown>({
  id: 'quality:dead-code',
  description: 'Detect unused files, exports, and deps via knip',

  handler: {
    async execute(ctx: PluginCommandContextV3, input: CLIInput<DeadCodeFlags>): Promise<CommandResult> {
      const { flags } = input;
      const config = await useConfig<QualityPluginConfig>();
      const cwd = ctx.cwd ?? process.cwd();
      const cfg = config ?? defaultQualityConfig;

      if (!cfg.knip.enabled) {
        ctx.ui?.success?.('knip is disabled in config (knip.enabled = false)');
        return { ok: true };
      }

      const report = await runKnip({ shell: ctx.api.shell, rootDir: cwd });

      if (flags.json) {
        return { ok: true, result: report };
        return report.totalIssues > 0
          ? { ok: false, error: { code: 'KB_RUNTIME_COMMAND_FAILED', message: 'Dead code found', hint: 'Run `kb quality --help` to review the command inputs, then rerun the command.' }, result: report }
          : { ok: true, result: report };
      }

      if (report.totalIssues === 0) {
        ctx.ui?.success?.('No dead code detected');
        return { ok: true };
      }

      const sections: Array<{ header: string; items: string[] }> = [];

      if (report.unusedFiles.length > 0) {
        sections.push({
          header: `Unused files (${report.unusedFiles.length})`,
          items: report.unusedFiles,
        });
      }

      if (report.unusedExports.length > 0) {
        sections.push({
          header: `Unused exports (${report.unusedExports.length})`,
          items: report.unusedExports.map(e => `${e.file}  ${e.symbol}`),
        });
      }

      if (report.unusedDependencies.length > 0) {
        sections.push({
          header: `Unused dependencies (${report.unusedDependencies.length})`,
          items: report.unusedDependencies.map(d => `${d.package}  in ${d.workspace}`),
        });
      }

      if (report.unlistedDependencies.length > 0) {
        sections.push({
          header: `Unlisted dependencies (${report.unlistedDependencies.length})`,
          items: report.unlistedDependencies.map(d => `${d.package}  in ${d.workspace}`),
        });
      }


      return { ok: false, error: { code: 'KB_RUNTIME_COMMAND_FAILED', message: `${report.totalIssues} dead code issue(s) found.`, hint: 'Review the reported unused files and dependencies, then rerun `kb quality dead-code`.' } };
    },
  },
});
