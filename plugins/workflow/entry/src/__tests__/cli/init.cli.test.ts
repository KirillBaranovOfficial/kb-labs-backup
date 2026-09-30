import { afterEach, describe, expect, it, vi } from 'vitest';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { mockCLIInput, createCapturedUI, createMockContext } from '@kb-labs/shared-testing-e2e/cli';
import initCommand from '../../commands/init.js';

describe('workflow:init', () => {
  let root: string | undefined;

  afterEach(async () => {
    vi.restoreAllMocks();
    if (root) { await rm(root, { recursive: true, force: true }); root = undefined; }
  });

  it('writes a no-network example and refuses to overwrite it', async () => {
    root = await mkdtemp(join(tmpdir(), 'workflow-init-'));
    vi.spyOn(process, 'cwd').mockReturnValue(root);

    const { ui, captured } = createCapturedUI();
    const ctx = createMockContext({ ui });
    const created = await initCommand.execute(ctx, mockCLIInput({ flags: {} }));
    const path = join(root, '.kb', 'workflows', 'hello.yml');
    const source = await readFile(path, 'utf8');
    expect(created.ok).toBe(true);
    expect(source).toContain('command: echo hello-workflow');
    expect(source).toContain('runsOn: local');
    expect(captured.success[0]?.message).toContain('hello.yml');

    const duplicate = await initCommand.execute(ctx, mockCLIInput({ flags: {} }));
    expect(duplicate.ok).toBe(false);
    expect(await readFile(path, 'utf8')).toBe(source);
    expect(captured.errors[0]).toContain('already exists');
  });
});
