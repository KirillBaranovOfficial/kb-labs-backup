import { beforeEach, describe, expect, it, vi } from 'vitest';
import { mockCLIInput, createCapturedUI, createMockContext } from '@kb-labs/sdk/testing';

vi.mock('@kb-labs/commit-core', () => ({
  loadPlan: vi.fn(),
  getCurrentPlanPath: vi.fn(),
  formatCommitMessage: vi.fn(),
}));

vi.mock('@kb-labs/sdk', async (importOriginal) => {
  const original = await importOriginal<typeof import('@kb-labs/sdk')>();
  return { ...original, findRepoRoot: vi.fn().mockResolvedValue('/project') };
});

import { loadPlan } from '@kb-labs/commit-core';
import commitOpenCommand from '../../src/cli/commands/open.js';

const mockedLoadPlan = vi.mocked(loadPlan);

describe('commit:open', () => {
  beforeEach(() => mockedLoadPlan.mockReset());

  it('keeps a malformed plan error hint attributable through commit open error handling', async () => {
    mockedLoadPlan.mockRejectedValueOnce(Object.assign(new Error('Plan could not be loaded.'), {
      code: 'COMMIT_PLAN_FAILED',
      hint: { key: 'errors.commit.open', params: { path: '.kb/commit/plan.json' } },
    }));

    const { ui } = createCapturedUI();
    const result = await commitOpenCommand.execute(createMockContext({ ui }), mockCLIInput({ flags: { json: true } }));

    expect(result).toMatchObject({
      ok: false,
      error: {
        code: 'KB_PLUGIN_CONTRACT_VIOLATION',
        details: {
          field: 'hint',
          observedType: 'object',
          observedValuePreview: expect.stringContaining('errors.commit.open'),
        },
      },
    });
  });
});
