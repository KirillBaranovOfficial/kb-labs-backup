import { describe, expect, it, vi } from 'vitest';
import { noopTraceContext, noopUI } from '@kb-labs/plugin-contracts';
import type { PluginContextV3 } from '@kb-labs/plugin-contracts';
import type { PlatformSyncResult } from '@kb-labs/core-runtime';

vi.mock('@kb-labs/core-runtime', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@kb-labs/core-runtime')>();
  return { ...actual, platformSync: vi.fn() };
});

import { platformSync } from '@kb-labs/core-runtime';
import { platformSyncCommand } from './sync.js';

describe('platform sync structured gate diagnostics', () => {
  it('uses each error message in both the envelope and diagnostics list', async () => {
    vi.mocked(platformSync).mockResolvedValue({
      ok: false,
      checked: 2,
      missing: [],
      mismatched: [],
      installed: [],
      errors: [
        { packageId: '@kb-labs/a', message: 'Package A failed.' },
        { packageId: '@kb-labs/b', message: 'Package B failed.' },
      ],
      mode: 'reconcile',
      lockMissing: false,
    } satisfies PlatformSyncResult);

    const json: unknown[] = [];
    const ctx = {
      host: 'cli',
      requestId: 'platform-sync-test',
      pluginId: '@kb-labs/system',
      pluginVersion: '1.0.0',
      cwd: '/workspace',
      ui: { ...noopUI, json: (value: unknown) => json.push(value) },
      platform: { logger: { info: vi.fn(), error: vi.fn(), warn: vi.fn(), debug: vi.fn(), child: vi.fn() } },
      runtime: { fs: {}, fetch: vi.fn(), env: vi.fn() },
      api: {},
      hostContext: { host: 'cli', binaryName: 'kb', argv: [], flags: {} },
      trace: noopTraceContext,
    } as unknown as PluginContextV3;

    const exitCode = await platformSyncCommand.run(ctx, [], { json: true, mode: 'reconcile' });

    expect(exitCode).toBe(1);
    expect(json).toEqual([{
      ok: false,
      error: expect.objectContaining({
        code: 'KB_RUNTIME_COMMAND_FAILED',
        message: 'Package A failed.; Package B failed.',
        diagnostics: [
          { code: 'KB_RUNTIME_COMMAND_FAILED', message: 'Package A failed.', severity: 'error' },
          { code: 'KB_RUNTIME_COMMAND_FAILED', message: 'Package B failed.', severity: 'error' },
        ],
      }),
    }]);
  });
});
