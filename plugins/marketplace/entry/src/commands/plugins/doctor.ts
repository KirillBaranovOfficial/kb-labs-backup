import { defineCommand, validationError, handleError, type PluginCommandContextV3, type CommandResult } from '@kb-labs/sdk';

import { get } from '../../http.js';
import { resolveCliScope, CliScopeError } from '../../scope.js';

interface DoctorFlags { json?: boolean; scope?: string }
interface DoctorInput { flags?: DoctorFlags }

interface DoctorIssue {
  severity: string;
  packageId: string;
  diagnosticCode?: string;
  message: string;
  remediation?: string;
}

interface DoctorResultData {
  ok: boolean;
  total: number;
  issues: DoctorIssue[];
}

export default defineCommand<unknown, DoctorInput, DoctorResultData>({
  id: 'marketplace:plugins:doctor',
  description: 'Diagnose marketplace health',

  handler: {
    async execute(ctx: PluginCommandContextV3, input: DoctorInput): Promise<CommandResult<DoctorResultData>> {
      const flags = (input.flags ?? input) as DoctorFlags;

      let scopeCtx;
      try {
        scopeCtx = await resolveCliScope(ctx.cwd, flags.scope);
      } catch (err) {
        if (err instanceof CliScopeError) {
          return validationError(ctx, err.message, undefined, flags.json);
        } else {
          return handleError(ctx, err, flags.json);
        }
              }

      try {
        const query: Record<string, string> = { scope: scopeCtx.scope };
        if (scopeCtx.projectRoot) { query.projectRoot = scopeCtx.projectRoot; }
        const report = await get<DoctorResultData>('/diagnostics', query);

        if (flags.json) {
          return report;
        } else if (report.issues.length === 0) {
          ctx.ui?.success?.(`All ${report.total} packages healthy (${scopeCtx.scope})`);
        } else {
          const errors = report.issues.filter(i => i.severity === 'error');
          ctx.ui?.warn?.(`Marketplace Doctor (${scopeCtx.scope})`, {
            sections: [
              { header: 'Summary', items: [`Total: ${report.total}`, `Issues: ${report.issues.length}`, `Errors: ${errors.length}`] },
              { header: 'Issues', items: report.issues.map(i => `${i.severity === 'error' ? '❌' : '⚠'} ${i.packageId}${i.diagnosticCode ? ` [${i.diagnosticCode}]` : ''}: ${i.message}`) },
            ],
          });
        }

        return report.ok ? report : {
          ok: false,
          error: {
            code: 'KB_RUNTIME_COMMAND_FAILED',
            message: 'Marketplace doctor found issues',
            hint: "Run `kb marketplace plugins doctor --help` to review its inputs and recovery steps, then retry.",
            diagnostics: report.issues.map((issue) => ({
              code: issue.diagnosticCode ?? 'KB_MARKETPLACE_DIAGNOSTIC',
              severity: issue.severity === 'error' ? 'error' as const : 'warning' as const,
              path: issue.packageId,
              message: issue.message,
              ...(issue.remediation ? { hint: issue.remediation } : {}),
            })),
          },
        };
      } catch (err) {
        return handleError(ctx, err, flags.json);
      }
    },
  },
});
