import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';

const checker = new URL('../check-marketplace-lock.mjs', import.meta.url);

async function runCheck(lock) {
  const root = await mkdtemp(join(tmpdir(), 'marketplace-lock-check-'));
  try {
    await mkdir(join(root, '.kb'), { recursive: true });
    await writeFile(join(root, '.kb', 'marketplace.lock'), JSON.stringify(lock));
    const result = spawnSync(process.execPath, [checker.pathname], {
      encoding: 'utf8',
      env: { ...process.env, KB_MARKETPLACE_LOCK_ROOT: root },
    });
    return { ...result, root };
  } catch (error) {
    await rm(root, { recursive: true, force: true });
    throw error;
  }
}

test('accepts a v3 project pin without machine paths', async () => {
  const result = await runCheck({
    schema: 'kb.marketplace/3',
    plugins: { '@scope/plugin': { version: '1.2.3', integrity: 'sha512-abc', package: '@scope/plugin', enabled: true, source: 'marketplace' } },
  });
  try {
    assert.equal(result.status, 0, result.stderr);
    assert.match(result.stdout, /schema v3/);
  } finally { await rm(result.root, { recursive: true, force: true }); }
});

test('rejects a v3 marketplace pin without its package name', async () => {
  const result = await runCheck({
    schema: 'kb.marketplace/3',
    plugins: { '@kb-labs/commit': { version: '1.2.3', integrity: 'sha512-abc', enabled: true, source: 'marketplace' } },
  });
  try {
    assert.equal(result.status, 1);
    assert.match(result.stderr, /must record its npm package name/);
  } finally { await rm(result.root, { recursive: true, force: true }); }
});

test('rejects a v3 platform pin without its package name', async () => {
  const result = await runCheck({
    schema: 'kb.marketplace/3',
    plugins: { '@kb-labs/commit': { version: '1.2.3', integrity: 'sha512-abc', enabled: true, source: 'platform' } },
  });
  try {
    assert.equal(result.status, 1);
    assert.match(result.stderr, /must record its npm package name/);
  } finally { await rm(result.root, { recursive: true, force: true }); }
});

test('reports a v2 lock with the regeneration command', async () => {
  const result = await runCheck({ schema: 'kb.marketplace/2', installed: {} });
  try {
    assert.equal(result.status, 1);
    assert.match(result.stderr, /kb marketplace sync/);
  } finally { await rm(result.root, { recursive: true, force: true }); }
});

test('rejects a machine path in a project pin', async () => {
  const result = await runCheck({
    schema: 'kb.marketplace/3',
    plugins: { demo: { version: '1.0.0', integrity: 'sha512-abc', package: 'demo-entry', enabled: true, source: 'marketplace', resolvedPath: '/machine/store/demo' } },
  });
  try {
    assert.equal(result.status, 1);
    assert.match(result.stderr, /machine-independent v3 pin/);
  } finally { await rm(result.root, { recursive: true, force: true }); }
});
