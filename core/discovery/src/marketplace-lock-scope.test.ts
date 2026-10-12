import { afterEach, describe, expect, it } from 'vitest';
import * as fs from 'node:fs/promises';
import * as os from 'node:os';
import * as path from 'node:path';
import { resolveMarketplaceLockScope, validateMarketplaceLockAtRoot } from './marketplace-lock-scope.js';

const rootsToRemove: string[] = [];

afterEach(async () => {
  await Promise.all(rootsToRemove.splice(0).map(root => fs.rm(root, { recursive: true, force: true })));
});

async function makeRoots(): Promise<{ platformRoot: string; projectRoot: string }> {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'marketplace-lock-scopes-'));
  rootsToRemove.push(root);
  const platformRoot = path.join(root, 'platform');
  const projectRoot = path.join(root, 'project');
  await Promise.all([fs.mkdir(platformRoot), fs.mkdir(projectRoot)]);
  return { platformRoot, projectRoot };
}

describe('marketplace lock root scope', () => {
  it('assigns a shared platform and project root to platform scope', async () => {
    const { platformRoot } = await makeRoots();
    const lock = { schema: 'kb.marketplace/3', plugins: {}, adapters: {} };

    expect(await resolveMarketplaceLockScope({ platformRoot, projectRoot: platformRoot }, platformRoot)).toBe('platform');
    expect((await validateMarketplaceLockAtRoot(lock, { platformRoot, projectRoot: platformRoot }, platformRoot)).valid).toBe(true);
  });

  it('continues to reject adapters for a distinct project root', async () => {
    const roots = await makeRoots();
    const lock = { schema: 'kb.marketplace/3', plugins: {}, adapters: {} };

    expect(await resolveMarketplaceLockScope(roots, roots.projectRoot)).toBe('project');
    expect(await validateMarketplaceLockAtRoot(lock, roots, roots.projectRoot)).toMatchObject({
      valid: false,
      reason: 'project lock must not contain adapters',
      scope: 'project',
    });
  });
});
