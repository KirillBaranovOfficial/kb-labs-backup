import { defineCommand, useConfig, type PluginCommandContextV3, type CommandResult } from '@kb-labs/sdk';
import type { PolicyConfig } from '@kb-labs/policy-contracts';

type RulesInput = {
  json?: boolean;
};

export default defineCommand({
  id: 'policy:rules',
  description: 'Show all policy rules and which categories they apply to',

  handler: {
    async execute(ctx: PluginCommandContextV3, input: RulesInput): Promise<CommandResult> {
      const flags = (input as { flags?: RulesInput }).flags ?? input;
      const policyConfig = await useConfig<PolicyConfig>();

      if (!policyConfig?.categories || !policyConfig?.rules) {
        return { ok: false, error: { code: 'KB_RUNTIME_COMMAND_FAILED', message: 'No policies config found', hint: "Run `kb policy rules --help` to review its inputs and recovery steps, then retry." } };
      }

      // Build reverse map: rule → categories that use it
      const ruleToCategories = new Map<string, string[]>();
      for (const [categoryName, categoryConfig] of Object.entries(policyConfig.categories)) {
        for (const rule of categoryConfig.rules) {
          const existing = ruleToCategories.get(rule) ?? [];
          existing.push(categoryName);
          ruleToCategories.set(rule, existing);
        }
      }

      if (flags.json) {
        const output = Object.entries(policyConfig.rules).map(([name, ruleDef]) => ({
          name,
          description: ruleDef.description,
          severity: ruleDef.severity,
          appliedTo: ruleToCategories.get(name) ?? [],
        }));
        return { ok: true, result: { rules: output } };
      } else {
        const items = Object.entries(policyConfig.rules).map(([name, ruleDef]) => {
          const categories = ruleToCategories.get(name) ?? [];
          const appliedTo = categories.length > 0 ? categories.join(', ') : '(none)';
          return `  ${name}\n    ${ruleDef.description}\n    Applied to: ${appliedTo}\n    Severity: ${ruleDef.severity}`;
        });
        ctx.ui.success?.('Policy Rules', { sections: [{ header: 'Rules', items }] });
      }

      return { ok: true };
    },
  },
});
