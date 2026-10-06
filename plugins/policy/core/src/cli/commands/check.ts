import { defineCommand, useConfig, findRepoRoot, type PluginCommandContextV3, type CommandResult } from '@kb-labs/sdk';
import { execSync } from 'node:child_process';
import type { PolicyConfig, CheckReport } from '@kb-labs/policy-contracts';
import { detectCategory } from '../../core/category-resolver.js';
import { runChecks } from '../../core/policy-runner.js';

type CheckInput = {
  path?: string;
  json?: boolean;
};

type CheckResult = CheckReport;

export default defineCommand({
  id: 'policy:check',
  description: 'Run policy checks for changed repos or a specific path. Exits with code 1 on violations.',

  handler: {
    async execute(ctx: PluginCommandContextV3, input: CheckInput): Promise<CommandResult<CheckResult>> {
      const flags = (input as { flags?: CheckInput }).flags ?? input;
      const workspaceRoot = (await findRepoRoot(ctx.cwd)) ?? ctx.cwd;
      const policyConfig = await useConfig<PolicyConfig>();

      if (!policyConfig?.categories) {
        const emptyReport: CheckReport = {
          passed: false,
          repos: [],
          summary: { total: 0, passed: 0, failed: 0, violations: 0 },
        };
        return { ok: false, error: { code: 'KB_RUNTIME_INPUT_INVALID', message: 'No policies config found in .kb/kb.config.json (expected "policies" key)', hint: 'Add a `policies` section to .kb/kb.config.json, then rerun `kb policy check`.' }, result: emptyReport };
      }

      const repoPaths = flags.path
        ? [flags.path]
        : detectChangedRepos(workspaceRoot);

      const repos = repoPaths.map((p) => detectCategory(p, policyConfig));

      // Warn about repos with no category
      for (const r of repos) {
        if (!r.category) {
          ctx.ui.warn?.(`Repo ${r.path} has no category — applying default rules: ${r.rules.join(', ')}`);
        }
      }

      const report = await runChecks(repos, policyConfig, workspaceRoot);

      if (flags.json) {
        return { ok: true, result: report };
      } else {
        renderHumanReport(ctx, report);
      }

      return report.passed
        ? { ok: true, result: report }
        : { ok: false, error: { code: 'KB_RUNTIME_COMMAND_FAILED', message: 'Policy violations found', hint: "Run `kb policy check --help` to review its inputs and recovery steps, then retry." }, result: report };
    },
  },
});

function renderHumanReport(ctx: PluginCommandContextV3, report: CheckReport): void {
  const lines: string[] = [];

  for (const repo of report.repos) {
    const cat = repo.category ?? '(no category)';
    lines.push(`\n${repo.path} (category: ${cat})`);

    for (const violation of repo.violations) {
      lines.push(`  ❌ ${violation.rule}`);
      lines.push(`     ${violation.message}`);
      if (violation.detail) {lines.push(`     → ${violation.detail}`);}
    }
    for (const passed of repo.passed) {
      lines.push(`  ✅ ${passed}`);
    }
  }

  const { total, passed, failed, violations } = report.summary;
  const summaryLine = report.passed
    ? `✅ All ${total} repo(s) passed`
    : `❌ ${violations} violation(s) found in ${failed}/${total} repo(s) — pipeline blocked`;

  if (report.passed) {
    ctx.ui.success?.('Policy Check', {
      sections: [
        { header: 'Results', items: lines },
        { header: 'Summary', items: [`${passed}/${total} passed`] },
      ],
    });
  }
}

function detectChangedRepos(workspaceRoot: string): string[] {
  try {
    const output = execSync('git diff --name-only HEAD 2>/dev/null || git diff --name-only', {
      cwd: workspaceRoot,
      encoding: 'utf-8',
      timeout: 10000,
    });

    const repoSet = new Set<string>();
    for (const file of output.trim().split('\n').filter(Boolean)) {
      const parts = file.split('/');
      if (parts.length >= 2) {
        const topDir = parts[0]!;
        const repoDir = parts[1]!;
        if (['platform', 'plugins', 'infra', 'templates', 'installer', 'sites'].includes(topDir)) {
          repoSet.add(`${topDir}/${repoDir}`);
        }
      }
    }
    return Array.from(repoSet);
  } catch {
    return [];
  }
}
