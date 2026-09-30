import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { prepareJourneyUpdatePackages } from './journey-update-packages.mjs';

test('creates real N+1 runtime and SDK package identities with unchanged code', () => {
  const root = mkdtempSync(join(tmpdir(), 'journey-update-packages-'));
  try {
    const packages = join(root, 'packages');
    const update = join(root, 'update');
    mkdirSync(packages);
    const fixtures = [
      ['@kb-labs/core-runtime', '3.0.0', 'core runtime bytes'],
      ['@kb-labs/sdk', '3.0.0', 'sdk bytes'],
      ['@kb-labs/host-app', '3.0.0', 'host bytes'],
    ];
    for (const [name, version, code] of fixtures) {
      const packageRoot = join(root, `${name.split('/').at(-1)}-source`, 'package');
      mkdirSync(packageRoot, { recursive: true });
      writeFileSync(join(packageRoot, 'package.json'), JSON.stringify({ name, version }));
      writeFileSync(join(packageRoot, 'index.js'), code);
      execFileSync('tar', ['-czf', join(packages, `${name.split('/').at(-1)}-${version}.tgz`), '-C', join(packageRoot, '..'), 'package']);
    }

    prepareJourneyUpdatePackages(packages, update, '3.0.1');

    for (const name of ['core-runtime', 'sdk']) {
      const original = join(packages, `${name}-3.0.0.tgz`);
      const upgraded = join(update, `${name}-3.0.1.tgz`);
      const packageJSON = JSON.parse(execFileSync('tar', ['-xOzf', upgraded, 'package/package.json'], { encoding: 'utf8' }));
      const originalCode = execFileSync('tar', ['-xOzf', original, 'package/index.js'], { encoding: 'utf8' });
      const upgradedCode = execFileSync('tar', ['-xOzf', upgraded, 'package/index.js'], { encoding: 'utf8' });
      assert.equal(packageJSON.version, '3.0.1');
      assert.equal(upgradedCode, originalCode);
      assert.equal(readFileSync(join(packages, `${name}-3.0.1.tgz`)).length > 0, true);
    }
    assert.equal(readFileSync(join(update, 'host-app-3.0.0.tgz')).length > 0, true);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('release-index Docker stage copies the package preparation helper', () => {
  const platformDirectory = dirname(fileURLToPath(import.meta.url));
  const dockerfile = readFileSync(join(platformDirectory, 'Dockerfile'), 'utf8');
  assert.match(dockerfile, /COPY e2e\/platform\/journey-update-packages\.mjs \/src\/journey-update-packages\.mjs/);
});
