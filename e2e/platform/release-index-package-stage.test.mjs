import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { clearGeneratedReleasePackages } from './release-index-package-stage.mjs';

test('clears only package tarballs listed by the previous release producer run', () => {
  const root = mkdtempSync(join(tmpdir(), 'kb-release-package-stage-'));
  try {
    const packages = join(root, 'packages');
    const output = join(root, 'release');
    const generated = join(output, 'update-packages');
    mkdirSync(packages);
    mkdirSync(generated, { recursive: true });
    writeFileSync(join(packages, 'kb-labs-gateway-app-2.120.1.tgz'), 'base');
    writeFileSync(join(packages, 'kb-labs-gateway-app-2.120.2.tgz'), 'previous update');
    writeFileSync(join(generated, 'generated-packages.txt'), 'kb-labs-gateway-app-2.120.2.tgz\n');

    clearGeneratedReleasePackages(packages, output);

    assert.equal(readFileSync(join(packages, 'kb-labs-gateway-app-2.120.1.tgz'), 'utf8'), 'base');
    assert.throws(() => readFileSync(join(packages, 'kb-labs-gateway-app-2.120.2.tgz')));
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
