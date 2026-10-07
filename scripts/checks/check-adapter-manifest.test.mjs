import assert from 'node:assert/strict';
import { mkdtemp, mkdir, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

const checker = fileURLToPath(new URL('./check-adapter-manifest.mjs', import.meta.url));

test('adapter-manifest reports schema and workspace category completeness errors', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'kb-adapter-manifest-check-'));
  try {
    const adapterRoot = path.join(root, 'packages', 'adapter');
    const extraRoot = path.join(root, 'packages', 'extra');
    await mkdir(path.join(adapterRoot, 'dist'), { recursive: true });
    await mkdir(path.join(extraRoot, 'dist'), { recursive: true });
    await writeFile(path.join(root, 'package.json'), JSON.stringify({ name: 'fixture-root', private: true }));
    await writeFile(path.join(root, 'pnpm-workspace.yaml'), 'packages: ["packages/*"]\n');
    await writeFile(path.join(root, 'devkit.yaml'), [
      'workspace:',
      '  categories:',
      '    adapter:',
      '      match: ["packages/adapter"]',
      '    ts-lib:',
      '      match: ["packages/*"]',
      '',
    ].join('\n'));
    await writeFixturePackage(adapterRoot, '@fixture/adapter', 'export const manifest = { implements: "IFixture" };\n');

    const missingSchema = await runCheck(root, adapterRoot, '@fixture/adapter');
    assertIssue(missingSchema, '@fixture/adapter', 'configSchema', 'dist/manifest.js');

    await writeFile(path.join(adapterRoot, 'dist', 'manifest.js'), 'export const manifest = { implements: "IFixture", configSchema: {} };\n');
    assert.deepEqual(await runCheck(root, adapterRoot, '@fixture/adapter'), []);

    await writeFixturePackage(extraRoot, '@fixture/extra', 'export const manifest = { implements: "IFixture", configSchema: {} };\n');
    const outsideCategory = await runCheck(root, adapterRoot, '@fixture/adapter');
    assertIssue(outsideCategory, '@fixture/extra', 'outside the devkit adapter category', 'dist/manifest.js');

    await writeFile(path.join(root, 'devkit.yaml'), [
      'workspace:',
      '  categories:',
      '    adapter:',
      '      match: ["packages/adapter", "packages/extra"]',
      '    ts-lib:',
      '      match: ["packages/*"]',
      '',
    ].join('\n'));
    await writeFile(path.join(extraRoot, 'dist', 'manifest.js'), 'export const manifest = { configSchema: {} };\n');
    const undiscoverableCategory = await runCheck(root, adapterRoot, '@fixture/adapter');
    assertIssue(undiscoverableCategory, '@fixture/extra', 'not discoverable by platform adapter discovery', 'dist/manifest.js');
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

async function writeFixturePackage(packageRoot, name, manifest) {
  await writeFile(path.join(packageRoot, 'package.json'), JSON.stringify({
    name,
    version: '1.0.0',
    type: 'module',
    kb: { manifest: './dist/manifest.js' },
  }));
  await writeFile(path.join(packageRoot, 'dist', 'manifest.js'), manifest);
}

async function runCheck(root, packageRoot, packageName) {
  const input = {
    workspaceRoot: root,
    check: 'adapter-manifest',
    package: {
      Name: packageName,
      Dir: packageRoot,
      RelPath: path.relative(root, packageRoot),
      Category: 'adapter',
    },
  };
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
