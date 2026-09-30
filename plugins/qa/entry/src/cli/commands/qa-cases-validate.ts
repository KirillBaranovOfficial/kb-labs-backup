import { defineCommand, useConfig, type CLIInput, type CommandResult, type PluginContextV3 } from '@kb-labs/sdk';
import { parseQACatalog, scanQACaseClaims } from '@kb-labs/qa-core';
import type { QAPluginConfig } from '@kb-labs/qa-contracts';
import type { QaCasesValidateFlags } from './flags.js';
import { resolveCatalogPath } from './qa-cases-utils.js';

export default defineCommand<unknown, CLIInput<QaCasesValidateFlags>, unknown>({
  id: 'qa:cases:validate',
  description: 'Validate the QA case catalog and test claims',
  handler: { async execute(ctx: PluginContextV3, input: CLIInput<QaCasesValidateFlags>): Promise<CommandResult> {
    const cwd = ctx.cwd ?? process.cwd();
    const config = await useConfig<QAPluginConfig>();
    const catalog = parseQACatalog(resolveCatalogPath(cwd, config?.casesPath));
    const claims = scanQACaseClaims(cwd, catalog.cases);
    const diagnostics = [...catalog.diagnostics, ...claims.diagnostics];
    const result = { schema: 'kb.qa-case-validation/v1', valid: diagnostics.length === 0, cases: catalog.cases.length, claims: claims.claims.length, diagnostics };
    if (input.flags.json) ctx.ui?.json?.(result);
    else if (diagnostics.length) ctx.ui?.error?.('QA case validation failed', { sections: [{ header: 'Fix these issues, then rerun `kb qa cases validate`', items: diagnostics.map(item => `${item.file}:${item.line}: ${item.message}`) }] });
    else ctx.ui?.success?.(`Validated ${catalog.cases.length} case(s) and ${claims.claims.length} test claim(s).`);
    return diagnostics.length ? { ok: false, error: 'QA case validation failed', result } : { ok: true, result };
  } },
});
