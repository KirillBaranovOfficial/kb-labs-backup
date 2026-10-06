import { defineCommand, type PluginCommandContextV3, type CommandResult } from '@kb-labs/sdk';
import {
  findWorkspaceRoot,
  detectChangedPackages,
  buildReverseDependencyGraph,
  analyzePackageImpact,
  analyzeTestImpact,
  analyzeBuildImpact,
  formatHumanReadable,
  generateRecommendations,
} from '../../core/index.js';

interface PkgFlags {
  json?: boolean;
}

interface PkgInput {
  argv: string[];
  flags: PkgFlags;
}

export default defineCommand<unknown, PkgInput, unknown>({
  id: 'impact:packages',
  description: 'Package dependency impact analysis',

  handler: {
    async execute(
      ctx: PluginCommandContextV3<unknown>,
      input: PkgInput,
    ): Promise<CommandResult<unknown>> {
      const flags = input.flags ?? (input as unknown as PkgFlags);

      let root: string;
      try {
        root = findWorkspaceRoot();
      } catch {
        if (flags.json) {return { ok: true, result: { packages: { direct: [], dependent: [], transitive: [] } } };}
        else {ctx.ui?.warn?.('Could not find workspace root');}
        return { ok: false, error: { code: 'KB_RUNTIME_COMMAND_FAILED', message: 'Could not find workspace root', hint: "Run `kb impact packages --help` to review its inputs and recovery steps, then retry." } };
      }

      const changed = detectChangedPackages(root);
      if (changed.length === 0) {
        const empty = { packages: { direct: [], dependent: [], transitive: [] }, tests: { mustRun: [], noTests: [] }, recommendations: [] };
        if (flags.json) {return { ok: true, result: empty };}
        else {ctx.ui?.success?.('No changes detected');}
        return { ok: true, result: empty };
      }

      const graph = buildReverseDependencyGraph(root);
      const packages = analyzePackageImpact(changed, graph);
      const allImpacted = [...packages.direct, ...packages.dependent, ...packages.transitive];
      const tests = analyzeTestImpact(allImpacted, root);
      const build = analyzeBuildImpact(allImpacted, graph);
      const recommendations = generateRecommendations(packages, { stale: [], review: [], reindex: [] });
      const result = { packages, tests, build, recommendations };

      if (flags.json) {
        return { ok: true, result: result };
      } else {
        ctx.ui?.write?.(formatHumanReadable({
          packages,
          docs: { stale: [], review: [], reindex: [] },
          tests,
          build,
          recommendations,
        }));
      }

      return { ok: true, result };
    },
  },
});
