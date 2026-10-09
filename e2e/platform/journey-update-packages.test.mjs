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

    const manifest = JSON.parse(readFileSync(join(update, 'manifest.json'), 'utf8'));
    assert.deepEqual(manifest.map(entry => [entry.name, entry.version, entry.tarball]).sort(), [
      ['@kb-labs/core-runtime', '3.0.1', 'core-runtime-3.0.1.tgz'],
      ['@kb-labs/host-app', '3.0.0', 'host-app-3.0.0.tgz'],
      ['@kb-labs/sdk', '3.0.1', 'sdk-3.0.1.tgz'],
    ]);
    assert.ok(manifest.every(entry => /^[0-9a-f]{64}$/.test(entry.sha256)));

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

test('builds a versioned failing service artifact for restart health recovery', () => {
  const root = mkdtempSync(join(tmpdir(), 'journey-failing-host-'));
  try {
    const packages = join(root, 'packages');
    const update = join(root, 'update');
    mkdirSync(packages);
    for (const [name, version, entry] of [
      ['@kb-labs/core-runtime', '3.0.0', ''],
      ['@kb-labs/sdk', '3.0.0', ''],
      ['@kb-labs/gateway-app', '3.0.0', 'dist/bin.js'],
    ]) {
      const packageRoot = join(root, `${name.split('/').at(-1)}-source`, 'package');
      mkdirSync(join(packageRoot, 'dist'), { recursive: true });
      writeFileSync(join(packageRoot, 'package.json'), JSON.stringify({ name, version }));
      if (entry) writeFileSync(join(packageRoot, entry), '#!/usr/bin/env node\nconsole.log("healthy");\n');
      execFileSync('tar', ['-czf', join(packages, `${name.split('/').at(-1)}-${version}.tgz`), '-C', join(packageRoot, '..'), 'package']);
    }

    prepareJourneyUpdatePackages(packages, update, '3.0.3', {
      publishDir: packages,
      failingServicePackage: '@kb-labs/gateway-app',
    });

    const manifest = JSON.parse(readFileSync(join(update, 'manifest.json'), 'utf8'));
    const failing = manifest.find(entry => entry.name === '@kb-labs/gateway-app');
    assert.equal(failing.version, '3.0.3');
    assert.match(execFileSync('tar', ['-xOzf', join(update, failing.tarball), 'package/dist/bin.js'], { encoding: 'utf8' }),
      /process\.exit\(1\)/);
    assert.equal(readFileSync(join(packages, failing.tarball)).length > 0, true);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('seals gateway packages with staged config and doctor failure requirements', () => {
  const root = mkdtempSync(join(tmpdir(), 'journey-staged-failure-'));
  try {
    const packages = join(root, 'packages');
    const update = join(root, 'update');
    mkdirSync(packages);
    for (const [name, version] of [
      ['@kb-labs/core-runtime', '3.0.0'],
      ['@kb-labs/sdk', '3.0.0'],
      ['@kb-labs/gateway-app', '3.0.0'],
    ]) {
      const packageRoot = join(root, `${name.split('/').at(-1)}-source`, 'package');
      mkdirSync(packageRoot, { recursive: true });
      writeFileSync(join(packageRoot, 'package.json'), JSON.stringify({ name, version }));
      if (name === '@kb-labs/gateway-app') {
        writeFileSync(join(packageRoot, 'kb-create.requirements.json'), JSON.stringify({
          schema: 'kb.create.requirements/v1', requirements: [],
        }));
      }
      execFileSync('tar', ['-czf', join(packages, `${name.split('/').at(-1)}-${version}.tgz`), '-C', join(packageRoot, '..'), 'package']);
    }
    const requirement = {
      id: 'journey.update.requiredConfig', path: '/platform/journey/updateRequired',
      required: true, hint: 'configure staged fixture',
    };
    prepareJourneyUpdatePackages(packages, update, '3.0.2', {
      publishDir: packages,
      requiredConfigPackage: '@kb-labs/gateway-app',
      requiredConfig: requirement,
    });
    const manifest = JSON.parse(readFileSync(join(update, 'manifest.json'), 'utf8'));
    const gateway = manifest.find(entry => entry.name === '@kb-labs/gateway-app');
    assert.equal(gateway.version, '3.0.2');
    const requirements = JSON.parse(execFileSync('tar', [
      '-xOzf', join(update, gateway.tarball), 'package/kb-create.requirements.json',
    ], { encoding: 'utf8' }));
    assert.deepEqual(requirements.requirements, [requirement]);
    assert.equal(readFileSync(join(packages, gateway.tarball)).length > 0, true);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
