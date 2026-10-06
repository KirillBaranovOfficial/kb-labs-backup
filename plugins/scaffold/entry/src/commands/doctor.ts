import { resolve } from 'node:path';
import { defineCommand, handleError, type PluginCommandContextV3, type CommandResult } from '@kb-labs/sdk';
import { scanRoot, type Finding } from '@kb-labs/scaffold-core';

interface DoctorFlags {
  path?: string;
  json?: boolean;
}

type DoctorResult = { root: string; packagesScanned: number; findings: Finding[] };

export default defineCommand({
  id: 'scaffold:doctor',
  description: 'Scan user-authored plugins for common issues',

  handler: {
    async execute(
      ctx: PluginCommandContextV3,
      input: DoctorFlags,
    ): Promise<CommandResult<DoctorResult>> {
      const root = resolve(input.path ?? '.kb/plugins');
      const workspaceRoot = ctx.cwd ?? process.cwd();
      let scan: Awaited<ReturnType<typeof scanRoot>>;
      try {
        scan = await scanRoot(root, { workspaceRoot });
      } catch (err) {
        return handleError(ctx, err, input.json);
      }

      const { errorCount, warnCount } = scan.findings.reduce(
        (acc, f) => {
          if (f.severity === 'error') { acc.errorCount++; }
          else if (f.severity === 'warn') { acc.warnCount++; }
          return acc;
        },
        { errorCount: 0, warnCount: 0 },
      );
      const hasErrors = errorCount > 0;
      const result = {
        root,
        packagesScanned: scan.packagesScanned,
        findings: scan.findings,
      };

      if (input.json) {
        return hasErrors
          ? { ok: false, error: { code: 'KB_RUNTIME_COMMAND_FAILED', message: `${errorCount} error(s) found`, hint: 'Inspect the error findings in the result and fix the affected plugin manifests, then rerun `kb scaffold doctor`.' }, result }
          : { ok: true, result };
      } else if (scan.findings.length === 0) {
        ctx.ui?.success?.(`Scanned ${scan.packagesScanned} package(s) — no issues found.`, {
          title: 'scaffold doctor',
          sections: [
            { items: [`Path: ${root}`] },
          ],
        });
      } else {
        const byPkg = new Map<string, Finding[]>();
        for (const f of scan.findings) {
          const list = byPkg.get(f.package) ?? [];
          list.push(f);
          byPkg.set(f.package, list);
        }

        const sections = Array.from(byPkg.entries()).map(([pkg, findings]) => ({
          header: pkg,
          items: findings.map((f) => `${f.severity.toUpperCase()}  ${f.message}`),
        }));

        if (hasErrors) {

        } else {
          ctx.ui?.warn?.(`Found ${warnCount} warning(s) in ${scan.packagesScanned} package(s).`, {
            title: 'scaffold doctor',
            sections,
          });
        }
      }

      return hasErrors ? {
        ok: false,
        error: { code: 'KB_RUNTIME_COMMAND_FAILED', message: `${errorCount} error(s) found`, hint: 'Inspect the error findings above and fix the affected plugin manifests, then rerun `kb scaffold doctor`.' },
        result,
      } : {
        ok: true,
        result,
      };
    },
  },
});
