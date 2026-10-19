import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, readFileSync, rmSync, utimesSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import test, { after } from 'node:test';
import { deriveCandidateVersion, prepareJourneyUpdatePackages, releasePackageNames } from './journey-update-packages.mjs';
import registryManifest from './registry-manifest.json' with { type: 'json' };

const digestToolDirectory = mkdtempSync(join(tmpdir(), 'kb-journey-digest-tool-'));
const digestTool = join(digestToolDirectory, 'kb-create-release-index');
execFileSync('go', ['build', '-trimpath', '-o', digestTool, './v2/cmd/kb-create-release-index'], {
  cwd: resolve(dirname(fileURLToPath(import.meta.url)), '../../tools/kb-create'),
});
after(() => rmSync(digestToolDirectory, { recursive: true, force: true }));

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
    writeFileSync(join(packageRoot, 'dist/index.js'), `${name} dist bytes`);
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

test('candidate version changes when packed package content changes', () => {
  const root = mkdtempSync(join(tmpdir(), 'journey-candidate-version-'));
  try {
    const first = join(root, 'first');
    const changed = join(root, 'changed');
    seedReleasePackages(root, first, registryManifest);
    seedReleasePackages(root, changed, registryManifest);
    const changedTarball = join(changed, packageFilename('@kb-labs/commit-entry', '3.0.0'));
    const unpack = join(root, 'unpack');
    mkdirSync(unpack);
    execFileSync('tar', ['-xzf', changedTarball, '-C', unpack]);
    const distBytes = readFileSync(join(unpack, 'package', 'dist/index.js'));
    distBytes[distBytes.length - 1] ^= 1;
    writeFileSync(join(unpack, 'package', 'dist/index.js'), distBytes);
    execFileSync('tar', ['-czf', changedTarball, '-C', unpack, 'package']);

    const firstVersion = deriveCandidateVersion('3.0.1', first, registryManifest, digestTool);
    const changedVersion = deriveCandidateVersion('3.0.1', changed, registryManifest, digestTool);
    assert.match(firstVersion, /^3\.0\.1-cand\.[0-9a-f]{12}$/);
    assert.notEqual(firstVersion, changedVersion);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('candidate version is idempotent when unchanged dist bytes are repacked with different mtimes', () => {
  const root = mkdtempSync(join(tmpdir(), 'journey-candidate-version-mtime-'));
  try {
    const first = join(root, 'first');
    const repacked = join(root, 'repacked');
    seedReleasePackages(root, first, registryManifest);
    seedReleasePackages(root, repacked, registryManifest);
    const filename = packageFilename('@kb-labs/commit-entry', '3.0.0');
    const archive = join(repacked, filename);
    const unpack = join(root, 'unpack');
    mkdirSync(unpack);
    execFileSync('tar', ['-xzf', archive, '-C', unpack]);
    const distFile = join(unpack, 'package', 'dist/index.js');
    utimesSync(distFile, new Date('2001-01-01T00:00:00Z'), new Date('2001-01-01T00:00:00Z'));
    execFileSync('tar', ['-czf', archive, '-C', unpack, 'package']);

    assert.equal(deriveCandidateVersion('3.0.1', first, registryManifest, digestTool),
      deriveCandidateVersion('3.0.1', repacked, registryManifest, digestTool));
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('versions every release-manifest artifact and preserves package code', () => {
  const root = mkdtempSync(join(tmpdir(), 'journey-update-packages-'));
  try {
    const packages = join(root, 'packages');
    const update = join(root, 'update');
    const packageNames = seedReleasePackages(root, packages, registryManifest, ['@kb-labs/not-in-release']);

    prepareJourneyUpdatePackages(packages, update, '3.0.1', { publishDir: packages, digestTool, registryManifest });

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
  assert.match(dockerfile, /COPY e2e\/platform\/package-provenance\.mjs \/src\/package-provenance\.mjs/);
});

test('builds a versioned failing service artifact for restart health recovery', () => {
  const root = mkdtempSync(join(tmpdir(), 'journey-failing-host-'));
  try {
    const packages = join(root, 'packages');
    const update = join(root, 'update');
    seedReleasePackages(root, packages);

    prepareJourneyUpdatePackages(packages, update, '3.0.3', {
      publishDir: packages,
      digestTool,
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
      digestTool,
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

test('refuses a staged requirement when its package is absent from the release manifest', () => {
  const root = mkdtempSync(join(tmpdir(), 'journey-staged-missing-package-'));
  try {
    const packages = join(root, 'packages');
    seedReleasePackages(root, packages);
    assert.throws(() => prepareJourneyUpdatePackages(packages, join(root, 'update'), '3.0.2', {
      publishDir: packages,
      digestTool,
      requiredConfigPackage: '@kb-labs/not-shipped',
      requiredConfig: { id: 'fixture.required', path: '/fixture/required', required: true },
      registryManifest,
    }), /staged requirement package is absent from the release manifest/);
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
      digestTool,
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
      digestTool,
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
