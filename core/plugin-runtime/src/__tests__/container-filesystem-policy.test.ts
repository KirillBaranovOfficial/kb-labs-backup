import { mkdtemp, mkdir, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { createFSShim } from '../runtime/fs-shim.js';

describe('container filesystem grant enforcement', () => {
  // case: S-244.1
  it('allows a wildcard match and denies a nonmatching file beneath the mounted prefix', async () => {
    const cwd = await mkdtemp(path.join(tmpdir(), 'kb-container-fs-'));
    try {
      await mkdir(path.join(cwd, 'src', 'nested'), { recursive: true });
      await writeFile(path.join(cwd, 'src', 'nested', 'allowed.ts'), 'export const allowed = true;');
      await writeFile(path.join(cwd, 'src', 'notes.txt'), 'not covered by the grant');
      const filesystem = createFSShim({
      pluginId: 'test-plugin',
        permissions: { fs: { read: ['src/**/*.ts'] } },
        cwd,
        strictPermissions: true,
      });

      await expect(filesystem.readFile('src/nested/allowed.ts')).resolves.toContain('allowed');
      await expect(filesystem.readFile('src/notes.txt')).rejects.toThrow(/Read access denied/);
    } finally {
      await rm(cwd, { recursive: true, force: true });
    }
  });
});
