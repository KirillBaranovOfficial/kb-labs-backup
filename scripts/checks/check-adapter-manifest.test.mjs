import assert from 'node:assert/strict';
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

const checker = fileURLToPath(new URL('./check-adapter-manifest.mjs', import.meta.url));

test('adapter-manifest uses devkit categories and validates built manifests', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'kb-adapter-manifest-check-'));
  try {
    const adapterRoot = path.join(root, 'adapters', 'adapter');
    const extraRoot = path.join(root, 'packages', 'extra');
    await writeFixturePackage(adapterRoot, '@fixture/adapter', 'export const manifest = { implements: "IFixture" };\n');
    await writeFixturePackage(extraRoot, '@fixture/extra', 'export const manifest = { implements: "IFixture", configSchema: {} };\n');

    const adapterPackage = { name: '@fixture/adapter', dir: adapterRoot, relPath: 'adapters/adapter', category: 'adapter' };
    const extraPackage = { name: '@fixture/extra', dir: extraRoot, relPath: 'packages/extra', category: 'ts-lib' };
    const payload = {
      workspaceRoot: root,
      check: 'adapter-manifest',
      package: { Name: adapterPackage.name, Dir: adapterRoot, RelPath: adapterPackage.relPath, Category: 'adapter' },
      workspace: { root, packages: [adapterPackage, extraPackage] },
    };

    const missingSchema = await runCheck(payload);
    assertIssue(missingSchema, '@fixture/adapter', 'configSchema', 'dist/manifest.js');

    await writeFile(path.join(adapterRoot, 'dist', 'manifest.js'), 'export const manifest = { implements: "IFixture", configSchema: {} };\n');
    const outsideCategory = await runCheck(payload);
    assertIssue(outsideCategory, '@fixture/extra', 'outside the devkit adapter category', 'package.json');

    extraPackage.category = 'adapter';
    await writeFile(path.join(extraRoot, 'dist', 'manifest.js'), 'export const manifest = { configSchema: {} };\n');
    const undiscoverableCategory = await runCheck(payload);
    assertIssue(undiscoverableCategory, '@fixture/extra', 'not discoverable by platform adapter discovery', 'package.json');
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

async function writeFixturePackage(packageRoot, name, manifest) {
  await mkdir(path.join(packageRoot, 'dist'), { recursive: true });
  await writeFile(path.join(packageRoot, 'package.json'), JSON.stringify({
    name,
    version: '1.0.0',
    type: 'module',
    kb: { manifest: './dist/manifest.js' },
  }));
  await writeFile(path.join(packageRoot, 'dist', 'manifest.js'), manifest);
}

async function runCheck(input) {
  const result = spawnSync(process.execPath, [checker], {
    cwd: path.dirname(checker),
    input: JSON.stringify(input),
    encoding: 'utf8',
  });
  assert.equal(result.status, 0, result.stderr);
  return JSON.parse(result.stdout).issues;
}

function assertIssue(issues, packageName, message, file) {
  const issue = issues.find(candidate => candidate.message.includes(packageName) && candidate.message.includes(message));
  assert.ok(issue, `missing issue ${packageName}: ${message}; got ${JSON.stringify(issues)}`);
  assert.ok(issue.file.endsWith(file), `issue file ${issue.file} does not end with ${file}`);
}
