import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';

test('Commit update fixture derives and packs the next major from Commit source', () => {
  const root = mkdtempSync(join(tmpdir(), 'kb-commit-update-fixture-test-'));
  try {
    const packages = join(root, 'packages');
    const source = join(root, 'source');
    mkdirSync(packages);
    mkdirSync(join(source, 'package'), { recursive: true });
    writeFileSync(join(source, 'package', 'package.json'), JSON.stringify({
      name: '@kb-labs/commit-entry', version: '2.120.1', files: ['manifest.js'],
    }));
    writeFileSync(join(source, 'package', 'manifest.js'), 'export const fixture = true;\n');
    execFileSync('tar', ['-czf', join(packages, 'kb-labs-commit-entry-2.120.1.tgz'), '-C', source, 'package']);

    execFileSync(process.execPath, [new URL('./prepare-commit-update-fixture.mjs', import.meta.url).pathname,
      '--packages-dir', packages]);

    const candidates = readdirSync(packages).filter(filename => filename.endsWith('.tgz'));
    assert.equal(candidates.length, 2);
    const versions = candidates.map(filename => JSON.parse(execFileSync('tar', [
      '-xOzf', join(packages, filename), 'package/package.json',
    ], { encoding: 'utf8' })).version).sort();
    assert.deepEqual(versions, ['2.120.1', '3.0.0']);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
