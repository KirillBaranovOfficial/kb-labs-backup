import { defineCommand, useConfig, type CLIInput, type CommandResult, type PluginContextV3 } from '@kb-labs/sdk';
import { parseQACatalog } from '@kb-labs/qa-core';
import type { QAPluginConfig } from '@kb-labs/qa-contracts';
import type { QaCasesListFlags } from './flags.js';
import { resolveCatalogPath } from './qa-cases-utils.js';

export default defineCommand<unknown, CLIInput<QaCasesListFlags>, unknown>({
  id: 'qa:cases:list',
  description: 'List cases in the QA catalog',
  handler: { async execute(ctx: PluginContextV3, input: CLIInput<QaCasesListFlags>): Promise<CommandResult> {
    const config = await useConfig<QAPluginConfig>();
    const catalog = parseQACatalog(resolveCatalogPath(ctx.cwd ?? process.cwd(), config?.casesPath));
    const result = { schema: 'kb.qa-case-list/v1', cases: catalog.cases, diagnostics: catalog.diagnostics };
    if (input.flags.json) ctx.ui?.json?.(result);
    else if (catalog.diagnostics.length) ctx.ui?.error?.('QA case catalog has errors', { sections: [{ header: 'Run `kb qa cases validate` for details', items: catalog.diagnostics.map(item => `${item.file}:${item.line}: ${item.message}`) }] });
    else ctx.ui?.success?.(`${catalog.cases.length} QA case(s)`, { sections: [{ items: catalog.cases.map(item => `${item.id} [${item.priority}] ${item.title} (${item.steps.length} steps)`) }] });
    return catalog.diagnostics.length ? { ok: false, error: 'QA case catalog is invalid', result } : { ok: true, result };
  } },
});
