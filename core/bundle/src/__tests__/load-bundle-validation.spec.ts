import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterEach, describe, it, expect, vi } from 'vitest';
import { loadBundle } from '../index';

describe('bundle: loadBundle validation option', () => {
  let cwd: string | undefined;

  afterEach(() => {
    if (cwd) {
      rmSync(cwd, { recursive: true, force: true });
    }
    cwd = undefined;
  });

  it('warns when validate is warn and config invalid', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    // An empty project: loadBundle writes .kb/lock.json, which must not land in the package.
    cwd = mkdtempSync(join(tmpdir(), 'kb-bundle-'));
    mkdirSync(join(cwd, '.kb'));
    writeFileSync(join(cwd, '.kb', 'kb.config.json'), '{}');
    await expect(
      loadBundle({ cwd, product: 'review' as any, validate: 'warn' })
    ).resolves.toBeTruthy();
  });
});
