import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createCapturedUI, createMockContext, mockCLIInput } from '@kb-labs/sdk/testing';

const mocks = vi.hoisted(() => ({
  analyzeLayering: vi.fn(),
  useConfig: vi.fn(),
}));

vi.mock('@kb-labs/quality-core', () => ({
  analyzeLayering: mocks.analyzeLayering,
}));

vi.mock('@kb-labs/sdk', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@kb-labs/sdk')>();
  return { ...actual, useConfig: mocks.useConfig };
});

import checkLayersCommand from '../../cli/commands/check-layers.js';

const violation = {
  file: 'src/cli.ts',
  fromPackage: '@kb-labs/core-runtime',
  fromLayer: 1,
  toPackage: '@kb-labs/studio',
  toLayer: 4,
  importSpecifier: '@kb-labs/studio',
};

beforeEach(() => {
  vi.resetAllMocks();
  mocks.useConfig.mockResolvedValue(undefined);
  mocks.analyzeLayering.mockResolvedValue({
    violations: [violation],
    totalViolations: 1,
    affectedPackages: ['@kb-labs/core-runtime'],
    layerMap: {},
  });
});

describe('quality:check-layers', () => {
  it('returns a typed failure with structured diagnostics when JSON findings exist', async () => {
    const { ui } = createCapturedUI();
    const ctx = createMockContext({ ui, cwd: '/project' });

    const result = await checkLayersCommand.execute(ctx as never, mockCLIInput({ flags: { json: true } }));

    expect(result).toMatchObject({
      ok: false,
      error: {
        code: 'KB_RUNTIME_COMMAND_FAILED',
        hint: 'Review the reported imports, then rerun `kb quality check layers`.',
        diagnostics: [{
          code: 'KB_QUALITY_LAYERING_VIOLATION',
          severity: 'error',
          path: 'src/cli.ts',
        }],
      },
    });
    expect(result).not.toHaveProperty('result');
  });
});
