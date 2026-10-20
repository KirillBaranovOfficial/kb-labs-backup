import { afterAll, describe, expect, it, vi } from 'vitest';
import { appendFile, mkdtemp, readFile, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { noopTraceContext, noopUI } from '@kb-labs/plugin-contracts';
import type { PluginContextV3 } from '@kb-labs/plugin-contracts';

const home = await mkdtemp(path.join(os.tmpdir(), 'kb-completion-test-'));
vi.stubEnv('HOME', home);
const { createCompletionCommand } = await import('../commands/system/completion.js');

afterAll(async () => {
  vi.unstubAllEnvs();
  await rm(home, { recursive: true, force: true });
});

function context(): PluginContextV3 {
  return {
    host: 'cli', requestId: 'completion-test', pluginId: '@kb-labs/system', pluginVersion: '1.0.0', cwd: process.cwd(),
    ui: { ...noopUI, json: vi.fn(), error: vi.fn() },
    platform: {}, runtime: { fs: {}, fetch: vi.fn(), env: vi.fn() }, api: {},
    hostContext: { host: 'cli', binaryName: 'kb', argv: [], flags: {} }, trace: noopTraceContext,
  } as unknown as PluginContextV3;
}

describe('kb completion removal', () => {
  // case: S-256.3
  it('removes installed zsh blocks and generated completion while preserving unrelated profile text', async () => {
    const command = createCompletionCommand({ complete: vi.fn((): string[] => []) } as unknown as Parameters<typeof createCompletionCommand>[0]);
    const ctx = context();
    expect(await command.run(ctx, ['install', 'zsh'], { json: true })).toBe(0);

    const profilePath = path.join(home, '.zshrc');
    const staticPath = path.join(home, '.config', 'kb', 'completion.zsh');
    await appendFile(profilePath, "\n# keep this user's alias\nalias keep-me=true\n");

    expect(await command.run(ctx, ['remove', 'zsh'], { json: true })).toBe(0);

    await expect(readFile(profilePath, 'utf8')).resolves.toBe("\n# keep this user's alias\nalias keep-me=true\n");
    await expect(readFile(staticPath, 'utf8')).rejects.toMatchObject({ code: 'ENOENT' });
  });
});
