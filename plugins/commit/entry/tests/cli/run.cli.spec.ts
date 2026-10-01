import { describe, it, expect, vi, beforeEach } from 'vitest';

vi.mock('@kb-labs/commit-core', () => ({
  generateCommitPlan: vi.fn(),
  savePlan: vi.fn().mockResolvedValue(undefined),
  hasChanges: vi.fn().mockReturnValue(true),
  getGitStatus: vi.fn().mockResolvedValue({ staged: [], unstaged: [] }),
  applyCommitPlan: vi.fn(),
  pushCommits: vi.fn(),
  saveToHistory: vi.fn().mockResolvedValue(undefined),
  clearPlan: vi.fn().mockResolvedValue(undefined),
}));

vi.mock('@kb-labs/sdk', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@kb-labs/sdk')>();
  return {
    ...actual,
    useLoader: vi.fn(() => ({
      start: vi.fn(),
      stop: vi.fn(),
      succeed: vi.fn(),
      fail: vi.fn(),
      update: vi.fn(),
    })),
    useConfig: vi.fn().mockResolvedValue({}),
    findRepoRoot: vi.fn().mockResolvedValue('/project'),
    useLLM: vi.fn().mockReturnValue(null),
  };
});

vi.mock('../../src/rest/handlers/scope-resolver.js', () => ({
  resolveScopePath: vi.fn((_cwd: string, _scope: string) => _cwd),
}));

import { generateCommitPlan, applyCommitPlan, hasChanges, getGitStatus, savePlan, saveToHistory, clearPlan } from '@kb-labs/commit-core';
import { AdapterCredentialsMissingError, AdapterUnavailableError } from '@kb-labs/sdk';
import { createCapturedUI, createMockContext, mockCLIInput } from '@kb-labs/sdk/testing';
import runCommand from '../../src/cli/commands/run.js';
import generateCommand from '../../src/cli/commands/generate.js';

const mockPlan = {
  commits: [
    {
      id: 'c1',
      type: 'feat',
      scope: 'auth',
      message: 'add login flow',
      files: ['src/auth.ts', 'src/login.ts'],
    },
    {
      id: 'c2',
      type: 'fix',
      scope: undefined,
      message: 'correct typo',
      files: ['README.md'],
    },
  ],
  metadata: {
    totalFiles: 3,
    totalCommits: 2,
    llmUsed: false,
    escalated: false,
    tokensUsed: 0,
  },
};

const mockApplyResult = {
  success: true,
  errors: [],
  appliedCommits: [
    { groupId: 'c1', sha: 'abc1234', message: 'feat(auth): add login flow' },
    { groupId: 'c2', sha: 'def5678', message: 'fix: correct typo' },
  ],
};

beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(getGitStatus).mockResolvedValue({ staged: [], unstaged: [] } as never);
  vi.mocked(hasChanges).mockReturnValue(true);
  vi.mocked(generateCommitPlan).mockResolvedValue(mockPlan as never);
  vi.mocked(savePlan).mockResolvedValue(undefined);
  vi.mocked(applyCommitPlan).mockResolvedValue(mockApplyResult as never);
  vi.mocked(saveToHistory).mockResolvedValue(undefined);
  vi.mocked(clearPlan).mockResolvedValue(undefined);
});

function createRunContext(ui: ReturnType<typeof createCapturedUI>['ui']) {
  return createMockContext({
    ui,
    cwd: '/project',
    platformConfig: { platform: { adapters: { llm: '@kb-labs/adapter-fixture' } } },
  });
}

describe('commit:commit (run)', () => {
  it('CR-01: applies commits — successful result and success message', async () => {
    const { ui, captured } = createCapturedUI();
    const ctx = createRunContext(ui);

    const result = await runCommand.execute(ctx as never, mockCLIInput({ flags: {} }));

    expect(result.ok).toBe(true);
    expect(captured.success.length).toBeGreaterThan(0);
    expect(vi.mocked(applyCommitPlan)).toHaveBeenCalledOnce();
  });

  it('CR-02: no changes — failed result and warning', async () => {
    vi.mocked(hasChanges).mockReturnValue(false);

    const { ui, captured } = createCapturedUI();
    const ctx = createRunContext(ui);

    const result = await runCommand.execute(ctx as never, mockCLIInput({ flags: {} }));

    expect(result.ok).toBe(false);
    expect(captured.warnings.length).toBeGreaterThan(0);
    expect(vi.mocked(applyCommitPlan)).not.toHaveBeenCalled();
  });

  it('CR-03: apply fails — failed result and error shown', async () => {
    vi.mocked(applyCommitPlan).mockResolvedValue({
      success: false,
      errors: ['Merge conflict'],
      appliedCommits: [],
    } as never);

    const { ui, captured } = createCapturedUI();
    const ctx = createRunContext(ui);

    const result = await runCommand.execute(ctx as never, mockCLIInput({ flags: {} }));

    expect(result.ok).toBe(false);
    expect(captured.errors.length).toBeGreaterThan(0);
  });

  it('CR-04: --json outputs CommitRunOutput', async () => {
    const { ui, captured } = createCapturedUI();
    const ctx = createRunContext(ui);

    const result = await runCommand.execute(ctx as never, mockCLIInput({ flags: { json: true } }));

    expect(result.ok).toBe(true);
    const out = captured.json[0] as Record<string, unknown>;
    expect(out.applied).toBe(true);
    expect(Array.isArray(out.commits)).toBe(true);
  });

  it('CR-05: --dry-run shows intent, applyCommitPlan is NOT called', async () => {
    const { ui, captured } = createCapturedUI();
    const ctx = createRunContext(ui);

    const result = await runCommand.execute(ctx as never, mockCLIInput({ flags: { 'dry-run': true } }));

    expect(result.ok).toBe(true);
    expect(vi.mocked(applyCommitPlan)).not.toHaveBeenCalled();
    expect(captured.infos[0]?.message).toContain('Dry-run');
    expect(captured.infos[0]?.message).toContain('2 commit');
  });

  it('CR-06: --dry-run with no changes — shows intent with empty operations', async () => {
    vi.mocked(hasChanges).mockReturnValue(false);

    const { ui, captured } = createCapturedUI();
    const ctx = createRunContext(ui);

    const result = await runCommand.execute(ctx as never, mockCLIInput({ flags: { 'dry-run': true } }));

    expect(result.ok).toBe(true);
    expect(vi.mocked(applyCommitPlan)).not.toHaveBeenCalled();
    expect(captured.infos[0]?.message).toContain('Dry-run');
    expect(captured.infos[0]?.message).toContain('No changes');
  });

  // case: S-160.2
  it('missing LLM adapter fails with the typed adapter error', async () => {
    const { ui } = createCapturedUI();
    const ctx = createMockContext({ ui, cwd: '/project' });

    await expect(runCommand.execute(ctx, mockCLIInput({ flags: { 'dry-run': true } })))
      .rejects.toBeInstanceOf(AdapterUnavailableError);
    expect(generateCommitPlan).not.toHaveBeenCalled();
    expect(applyCommitPlan).not.toHaveBeenCalled();
  });
});

describe('commit:generate (adapter errors)', () => {
  it('returns the typed adapter envelope in JSON mode when the LLM is unconfigured', async () => {
    vi.mocked(generateCommitPlan).mockRejectedValueOnce(new AdapterUnavailableError('llm'));

    const { ui, captured } = createCapturedUI();
    const ctx = createRunContext(ui);
    const result = await generateCommand.execute(ctx as never, mockCLIInput({ flags: { json: true } }));

    expect(result.ok).toBe(false);
    expect(captured.json[0]).toMatchObject({
      ok: false,
      error: {
        code: 'KB_ADAPTER_NOT_CONFIGURED',
        details: { service: 'llm' },
        actions: [{ command: 'kb adapter bind llm @kb-labs/adapters-openai' }],
      },
    });
  });

  // case: S-054.8
  it('returns a typed missing OpenAI key envelope in JSON mode when OpenAI is bound', async () => {
    vi.mocked(generateCommitPlan).mockRejectedValueOnce(new AdapterCredentialsMissingError(
      'llm', 'OPENAI_API_KEY', 'platform.adapterOptions.llm.apiKey',
    ));

    const { ui, captured } = createCapturedUI();
    const ctx = createMockContext({
      ui,
      cwd: '/project',
      platformConfig: { platform: { adapters: { llm: '@kb-labs/adapters-openai' } } },
    });
    const result = await generateCommand.execute(ctx as never, mockCLIInput({ flags: { json: true } }));

    expect(result.ok).toBe(false);
    expect(captured.json[0]).toMatchObject({
      ok: false,
      error: {
        code: 'KB_ADAPTER_CREDENTIALS_MISSING',
        message: expect.stringContaining('OPENAI_API_KEY'),
        hint: expect.stringContaining("kb config set platform.adapterOptions.llm.apiKey '${OPENAI_API_KEY}' --allow-missing-env"),
      },
    });
    expect(captured.json[0]).toMatchObject({ error: { hint: expect.stringContaining('No request was sent') } });
  });
});
