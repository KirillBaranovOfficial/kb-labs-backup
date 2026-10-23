import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const workspaceRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../..');
const checkScript = path.join(workspaceRoot, 'scripts/checks/check-plugin-package-identity.mjs');

test('plugin package identity check rejects a package name that differs from its manifest id', async (t) => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'kb-plugin-package-identity-'));
  t.after(async () => rm(root, { recursive: true, force: true }));
  const packageRoot = path.join(root, 'plugins', 'example', 'entry');
  await mkdir(packageRoot, { recursive: true });
  await writeFile(path.join(packageRoot, 'package.json'), JSON.stringify({
    name: '@example/example-entry',
    kb: { manifest: './manifest.mjs' },
  }));
  await writeFile(path.join(packageRoot, 'manifest.mjs'), `export const manifest = { schema: 'kb.plugin/3', id: '@example/example' };`);

  const input = {
    check: 'plugin-package-identity',
    workspaceRoot: root,
    workspace: { packages: [
      { name: '@example/example-entry', dir: 'plugins/example/entry', language: 'typescript' },
      { name: 'clikit', dir: 'tools/clikit', language: 'go' },
    ] },
  };
  const mismatch = spawnSync(process.execPath, [checkScript], {
    cwd: workspaceRoot,
    encoding: 'utf8',
    input: JSON.stringify(input),
  });
  assert.equal(mismatch.status, 0, mismatch.stderr);
  const mismatchIssues = JSON.parse(mismatch.stdout).issues;
  assert.deepEqual(mismatchIssues.map(({ message }) => message), [
    'plugin package name "@example/example-entry" differs from manifest identity "@example/example"',
  ]);
  assert.equal(
    mismatchIssues[0].fix,
    `pnpm --dir ${JSON.stringify(packageRoot)} pkg set name="@example/example"`,
  );

  const manifestPath = path.join(packageRoot, 'manifest.mjs');
  const manifest = await readFile(manifestPath, 'utf8');
  await writeFile(manifestPath, manifest.replace("'@example/example'", "'@example/example-entry'"));
  const aligned = spawnSync(process.execPath, [checkScript], {
    cwd: workspaceRoot,
    encoding: 'utf8',
    input: JSON.stringify(input),
  });
  assert.equal(aligned.status, 0, aligned.stderr);
  assert.deepEqual(JSON.parse(aligned.stdout).issues, []);
});
