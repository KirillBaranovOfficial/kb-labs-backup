/**
 * Release report command
 */

import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { defineCommand, type CLIInput, type CommandResult, type PluginCommandContextV3 } from '@kb-labs/sdk';
import type { ReleaseReport } from '@kb-labs/release-manager-core';
import { findRepoRoot } from '../../shared/utils';

interface ReportFlags {
  json?: boolean;
}

type ReleaseReportResult = CommandResult<unknown>;

// ── helpers ────────────────────────────────────────────────────────────────

function buildReportSections(report: ReleaseReport): Array<{ header?: string; items: string[] }> {
  const sections: Array<{ header?: string; items: string[] }> = [
    {
      header: 'Summary',
      items: [
        `Timestamp: ${report.ts}`,
        `Stage: ${report.stage}`,
        `Result: ${report.result.ok ? 'SUCCESS' : 'FAILED'}`,
      ],
    },
  ];

  if (report.result.errors && report.result.errors.length > 0) {
    sections.push({ header: 'Errors', items: [...report.result.errors] });
  }

  return sections;
}

function handleReportNotFound(flags: ReportFlags, ctx: PluginCommandContextV3): ReleaseReportResult {
  ctx.platform?.logger?.warn?.('No release report found');
  if (flags.json) {
    return { ok: false, error: { code: 'KB_RUNTIME_COMMAND_FAILED', message: 'No release report found', hint: "Run `kb release report --help` to review the failure and next steps." } };
  } else {

  }
  return { ok: false, error: { code: 'KB_RUNTIME_COMMAND_FAILED', message: 'No release report found', hint: "Run `kb release report --help` to review its inputs and recovery steps, then retry." } };
}

// ── command ────────────────────────────────────────────────────────────────

export default defineCommand({
  id: 'release:report',
  description: 'Show last release report',

  handler: {
    async execute(ctx: PluginCommandContextV3, input: CLIInput<ReportFlags>): Promise<ReleaseReportResult> {
      const { flags } = input;
      const cwd = ctx.cwd || process.cwd();
      const repoRoot = await findRepoRoot(cwd);
      const reportPath = join(repoRoot, '.kb', 'release', 'report.json');

      try {
        const reportContent = await readFile(reportPath, 'utf-8');
        const report = JSON.parse(reportContent) as ReleaseReport;

        ctx.platform?.logger?.info?.('Release report completed', {
          stage: report.stage,
          ok: report.result.ok,
        });

        if (flags.json) {
          return report;
        } else {
          if (!ctx.ui) { throw new Error('UI not available'); }
          ctx.ui?.sideBox?.({
            title: 'Release Report',
            sections: buildReportSections(report),
            status: report.result.ok ? 'success' : 'error',
          });
        }

        return report.result.ok ? { report } : { ok: false, error: { code: 'KB_RUNTIME_COMMAND_FAILED', message: 'Command failed', hint: "Run `kb release report --help` to review its inputs and recovery steps, then retry." } };
      } catch (error) {
        if ((error as NodeJS.ErrnoException).code === 'ENOENT') {
          return handleReportNotFound(flags, ctx);
        }
        throw error;
      }
    },
  },
});
