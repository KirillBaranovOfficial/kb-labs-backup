import { describe, expect, it, vi } from 'vitest';
import type { PluginContextV3 } from '@kb-labs/plugin-contracts';
import { configSet } from './set.js';

describe('config set project path diagnosis', () => {
  // case: S-204.12
  it('names the invalid scope path and gives the corrected runnable command', async () => {
    const json = vi.fn();
    const exitCode = await configSet.run(
      {
        requestId: 'config-path-case',
        hostContext: { host: 'cli', binaryName: 'kb', argv: [], flags: {} },
        ui: { json, error: vi.fn() },
        platform: { logger: { info: vi.fn(), error: vi.fn() } },
      } as unknown as PluginContextV3,
      ['commit.llm.enabled', 'true'],
      {
        scope: 'project',
        'dry-run': false,
        string: false,
        'allow-plain-secret': false,
        'allow-missing-env': false,
        json: true,
      },
    );
    expect(exitCode).not.toBe(0);
    expect(json).toHaveBeenCalledWith(expect.objectContaining({
      ok: false,
      error: expect.objectContaining({
        code: 'KB_CONFIG_INVALID',
        correlationId: 'config-path-case',
        message: 'Configuration path commit.llm.enabled is not valid at project scope; project plugin settings must use plugins.<configSection>.<key>.',
        hint: 'Run "kb config set plugins.commit.llm.enabled true" to write the project setting.',
      }),
    }));
  });
});
