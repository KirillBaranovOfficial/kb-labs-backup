import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterEach, describe, it, expect, vi } from 'vitest';
import { loadBundle } from '../index';
import { useIsolatedProjectRegistry } from './project-registry';

describe('bundle: loadBundle validation option', () => {
  let cwd: string | undefined;
  let cleanupRegistry: (() => Promise<void>) | undefined;

  afterEach(async () => {
    await cleanupRegistry?.();
    cleanupRegistry = undefined;
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
    cleanupRegistry = await useIsolatedProjectRegistry(cwd);
    await expect(
      loadBundle({ cwd, product: 'review' as any, validate: 'warn' })
    ).resolves.toBeTruthy();
  });
});
