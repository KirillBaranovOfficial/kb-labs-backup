import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../src/core/index.js', () => ({
  findWorkspaceRoot: vi.fn(() => { throw new Error('workspace root unavailable'); }),
  detectChangedPackages: vi.fn(),
  buildReverseDependencyGraph: vi.fn(),
  analyzePackageImpact: vi.fn(),
  generateRecommendations: vi.fn(),
  analyzeDocImpact: vi.fn(),
  analyzeTestImpact: vi.fn(),
  analyzeBuildImpact: vi.fn(),
  loadImpactRules: vi.fn(),
  formatHumanReadable: vi.fn(),
}));

import checkCommand from '../../src/cli/commands/check.js';
import docsCommand from '../../src/cli/commands/docs.js';
import packagesCommand from '../../src/cli/commands/packages.js';

const commands = [
  ['impact:check', checkCommand],
  ['impact:docs', docsCommand],
  ['impact:packages', packagesCommand],
] as const;

describe('impact command failure parity', () => {
  beforeEach(() => vi.clearAllMocks());

  for (const [id, command] of commands) {
    // case: S-203.1
    it(`${id} returns the workspace failure in both output modes`, async () => {
      const ctx = { host: 'cli', ui: { warn: vi.fn() } } as never;
      const humanResult = await command.execute(ctx, { flags: {} } as never);
      const jsonResult = await command.execute(ctx, { flags: { json: true } } as never);

      expect(humanResult).toMatchObject({ ok: false, error: { code: 'KB_RUNTIME_COMMAND_FAILED' } });
      expect(jsonResult).toEqual(humanResult);
    });
  }
});
