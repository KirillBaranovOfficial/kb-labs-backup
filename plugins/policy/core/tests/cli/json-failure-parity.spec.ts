import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createCapturedUI, createMockContext, mockCLIInput } from '@kb-labs/sdk/testing';

vi.mock('@kb-labs/sdk', async (importOriginal) => ({
  ...await importOriginal<typeof import('@kb-labs/sdk')>(),
  findRepoRoot: vi.fn().mockResolvedValue('/project'),
  useConfig: vi.fn().mockResolvedValue({ categories: {} }),
}));
vi.mock('../../src/core/category-resolver.js', () => ({
  detectCategory: vi.fn(() => ({ path: 'packages/app', category: 'app', rules: ['required'] })),
}));
vi.mock('../../src/core/policy-runner.js', () => ({
  runChecks: vi.fn().mockResolvedValue({
    passed: false,
    repos: [{ path: 'packages/app', category: 'app', rules: [], passed: [], violations: [{ rule: 'required', message: 'Required file is missing' }] }],
    summary: { total: 1, passed: 0, failed: 1, violations: 1 },
  }),
}));

import checkCommand from '../../src/cli/commands/check.js';

describe('policy:check JSON failure parity', () => {
  beforeEach(() => vi.clearAllMocks());

  // case: S-203.1
  it('returns a typed policy failure in both human and JSON modes', async () => {
    const { ui } = createCapturedUI();
    const ctx = createMockContext({ ui, cwd: '/project' });
    const humanResult = await checkCommand.execute(ctx, mockCLIInput({ flags: { path: 'packages/app' } }));
    const jsonResult = await checkCommand.execute(ctx, mockCLIInput({ flags: { path: 'packages/app', json: true } }));

    expect(humanResult).toMatchObject({ ok: false, error: { code: 'KB_RUNTIME_COMMAND_FAILED' } });
    expect(jsonResult).toEqual(humanResult);
  });
});
