import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { prepareJourneyUpdatePackages, releasePackageNames } from './journey-update-packages.mjs';
import registryManifest from './registry-manifest.json' with { type: 'json' };

function packageFilename(name, version) {
  return `${name.replace(/^@/, '').replaceAll('/', '-')}-${version}.tgz`;
}

function seedReleasePackages(root, packages, manifest = registryManifest, extraNames = []) {
  mkdirSync(packages, { recursive: true });
  const packageNames = [...new Set([...releasePackageNames(manifest), ...extraNames])];
  for (const name of packageNames) {
    const packageRoot = join(root, `${name.replace(/^@/, '').replaceAll('/', '-')}-source`, 'package');
    mkdirSync(join(packageRoot, 'dist'), { recursive: true });
    writeFileSync(join(packageRoot, 'package.json'), JSON.stringify({ name, version: '3.0.0' }));
    writeFileSync(join(packageRoot, 'index.js'), `${name} bytes`);
    if (name === '@kb-labs/gateway-app') {
      writeFileSync(join(packageRoot, 'dist/bin.js'), '#!/usr/bin/env node\nconsole.log("healthy");\n');
      writeFileSync(join(packageRoot, 'kb-create.requirements.json'), JSON.stringify({
        schema: 'kb.create.requirements/v1', requirements: [],
      }));
    }
    execFileSync('tar', ['-czf', join(packages, packageFilename(name, '3.0.0')), '-C', join(packageRoot, '..'), 'package']);
  }
  return packageNames;
}

test('versions every release-manifest artifact and preserves package code', () => {
  const root = mkdtempSync(join(tmpdir(), 'journey-update-packages-'));
  try {
    const packages = join(root, 'packages');
    const update = join(root, 'update');
    const packageNames = seedReleasePackages(root, packages, registryManifest, ['@kb-labs/not-in-release']);

    prepareJourneyUpdatePackages(packages, update, '3.0.1', { publishDir: packages, registryManifest });

    const manifest = JSON.parse(readFileSync(join(update, 'manifest.json'), 'utf8'));
    const versionedNames = releasePackageNames(registryManifest);
    assert.deepEqual(manifest.map(entry => [entry.name, entry.version]).sort((a, b) => a[0].localeCompare(b[0])),
      [...versionedNames.map(name => [name, '3.0.1']), ['@kb-labs/not-in-release', '3.0.0']].sort((a, b) => a[0].localeCompare(b[0])));
    assert.ok(manifest.every(entry => /^[0-9a-f]{64}$/.test(entry.sha256)));

    for (const packageName of versionedNames) {
      const original = join(packages, packageFilename(packageName, '3.0.0'));
      const upgraded = join(update, packageFilename(packageName, '3.0.1'));
      const packageJSON = JSON.parse(execFileSync('tar', ['-xOzf', upgraded, 'package/package.json'], { encoding: 'utf8' }));
      const originalCode = execFileSync('tar', ['-xOzf', original, 'package/index.js'], { encoding: 'utf8' });
      const upgradedCode = execFileSync('tar', ['-xOzf', upgraded, 'package/index.js'], { encoding: 'utf8' });
      assert.equal(packageJSON.version, '3.0.1');
      assert.equal(upgradedCode, originalCode);
      const publishedTarball = join(packages, packageFilename(packageName, '3.0.1'));
      assert.equal(readFileSync(publishedTarball).length > 0, true);
      const publishedPackage = JSON.parse(execFileSync('tar', [
        '-xOzf', publishedTarball, 'package/package.json',
      ], { encoding: 'utf8' }));
      assert.equal(publishedPackage.version, '3.0.1');
    }
    const untouched = JSON.parse(execFileSync('tar', [
      '-xOzf', join(update, packageFilename('@kb-labs/not-in-release', '3.0.0')), 'package/package.json',
    ], { encoding: 'utf8' }));
    assert.equal(untouched.version, '3.0.0');
    assert.equal(readFileSync(join(update, packageFilename('@kb-labs/not-in-release', '3.0.0'))).length > 0, true);
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
    seedReleasePackages(root, packages);

    prepareJourneyUpdatePackages(packages, update, '3.0.3', {
      publishDir: packages,
      failingServicePackage: '@kb-labs/gateway-app',
      registryManifest,
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
    seedReleasePackages(root, packages);
    const requirement = {
      id: 'journey.update.requiredConfig', path: '/platform/journey/updateRequired',
      required: true, hint: 'configure staged fixture',
    };
    prepareJourneyUpdatePackages(packages, update, '3.0.2', {
      publishDir: packages,
      requiredConfigPackage: '@kb-labs/gateway-app',
      requiredConfig: requirement,
      registryManifest,
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


test('release package set follows the manifest when a plugin is removed or added', () => {
  const root = mkdtempSync(join(tmpdir(), 'journey-update-manifest-mutation-'));
  try {
    const withoutCommit = structuredClone(registryManifest);
    withoutCommit.plugins = withoutCommit.plugins.filter(plugin => plugin.pkg !== '@kb-labs/commit-entry');
    const droppedPackages = join(root, 'dropped-packages');
    const droppedUpdate = join(root, 'dropped-update');
    const withoutNames = seedReleasePackages(root, droppedPackages, withoutCommit);
    prepareJourneyUpdatePackages(droppedPackages, droppedUpdate, '3.0.1', {
      publishDir: droppedPackages,
      registryManifest: withoutCommit,
    });
    const droppedNames = JSON.parse(readFileSync(join(droppedUpdate, 'manifest.json'), 'utf8')).map(item => item.name);
    assert.equal(withoutNames.includes('@kb-labs/commit-entry'), false);
    assert.equal(droppedNames.includes('@kb-labs/commit-entry'), false);
    assert.ok(droppedNames.every(name => withoutNames.includes(name)));

    const withNewPlugin = structuredClone(withoutCommit);
    withNewPlugin.plugins.push({ id: '@kb-labs/new-first-party', pkg: '@kb-labs/new-first-party' });
    const addedPackages = join(root, 'added-packages');
    const addedUpdate = join(root, 'added-update');
    const withNames = seedReleasePackages(root, addedPackages, withNewPlugin);
    prepareJourneyUpdatePackages(addedPackages, addedUpdate, '3.0.2', {
      publishDir: addedPackages,
      registryManifest: withNewPlugin,
    });
    const addedEntries = JSON.parse(readFileSync(join(addedUpdate, 'manifest.json'), 'utf8'));
    assert.equal(withNames.includes('@kb-labs/new-first-party'), true);
    assert.equal(addedEntries.find(item => item.name === '@kb-labs/new-first-party')?.version, '3.0.2');
    assert.equal(addedEntries.some(item => item.name === '@kb-labs/commit-entry'), false);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
