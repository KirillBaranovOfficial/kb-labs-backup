import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { copyFileSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { basename, join, resolve } from 'node:path';
import { existsSync } from 'node:fs';
import registryManifest from './registry-manifest.json' with { type: 'json' };

export function releasePackageNames(manifest = registryManifest) {
  return [...new Set([
    ...(manifest.core ?? []).map(item => item.name),
    ...(manifest.services ?? []).map(item => item.pkg),
    ...(manifest.plugins ?? []).map(item => item.pkg),
    ...(manifest.adapters ?? []).map(item => item.name),
  ].filter(Boolean))];
}

// Build a release stage for a journey-only platform version from an existing
// stage: every selected release artifact is reused unchanged except its
// package version, which advances to the requested update version. The target
// holds the same manifest.json the
// release-index preparer reads; generated tarballs are also written to
// publishDir so the local registry can serve them.
export function prepareJourneyUpdatePackages(packagesDir, updatePackagesDir, updateVersion,
  { publishDir, failingServicePackage, requiredConfigPackage, requiredConfig, registryManifest: releaseManifest = registryManifest } = {}) {
  const versionedPackages = new Set(releasePackageNames(releaseManifest));
  const sourceDir = resolve(packagesDir);
  const targetDir = resolve(updatePackagesDir);
  const registryDir = resolve(publishDir ?? packagesDir);
  const manifest = [];
  rmSync(targetDir, { recursive: true, force: true });
  mkdirSync(targetDir, { recursive: true });
  const originals = new Map();
  const generatedFiles = [];
  for (const filename of readdirSync(sourceDir).filter(name => name.endsWith('.tgz'))) {
    const source = join(sourceDir, filename);
    const packageJSON = JSON.parse(execFileSync('tar', ['-xOzf', source, 'package/package.json'], { encoding: 'utf8' }));
    if (versionedPackages.has(packageJSON.name)
        && packageJSON.name !== failingServicePackage
        && !(packageJSON.name === requiredConfigPackage && requiredConfig)) {
      originals.set(packageJSON.name, { source, filename, packageJSON });
    } else if (packageJSON.name === failingServicePackage) {
      const unpacked = mkdtempSync('/tmp/kb-e2e-update-package-');
      try {
        execFileSync('tar', ['-xzf', source, '-C', unpacked]);
        const packageRoot = join(unpacked, 'package');
        const packageJSONPath = join(packageRoot, 'package.json');
        const packageManifest = JSON.parse(readFileSync(packageJSONPath, 'utf8'));
        packageManifest.version = updateVersion;
        writeFileSync(packageJSONPath, `${JSON.stringify(packageManifest, null, 2)}\n`);
        writeFileSync(join(packageRoot, 'dist/bin.js'),
          '#!/usr/bin/env node\nprocess.stderr.write("journey health-failure fixture\\n");\nprocess.exit(1);\n');
        const filename = filenameFor(packageJSON.name, updateVersion);
        const updateTarball = join(targetDir, filename);
        execFileSync('tar', ['-czf', updateTarball, '-C', unpacked, 'package']);
        copyFileSync(updateTarball, join(registryDir, filename));
        manifest.push({ name: packageJSON.name, version: updateVersion, tarball: filename, sha256: sha256(updateTarball) });
        generatedFiles.push(filename);
      } finally {
        rmSync(unpacked, { recursive: true, force: true });
      }
    } else if (packageJSON.name === requiredConfigPackage && requiredConfig) {
      const unpacked = mkdtempSync('/tmp/kb-e2e-update-package-');
      try {
        execFileSync('tar', ['-xzf', source, '-C', unpacked]);
        const packageRoot = join(unpacked, 'package');
        const packageJSONPath = join(packageRoot, 'package.json');
        const changedPackageJSON = JSON.parse(readFileSync(packageJSONPath, 'utf8'));
        changedPackageJSON.version = updateVersion;
        writeFileSync(packageJSONPath, `${JSON.stringify(changedPackageJSON, null, 2)}\n`);
        const requirementPath = join(packageRoot, 'kb-create.requirements.json');
        const requirements = existsSync(requirementPath)
          ? JSON.parse(readFileSync(requirementPath, 'utf8'))
          : { schema: 'kb.create.requirements/v1', requirements: [] };
        requirements.requirements.push(requiredConfig);
        writeFileSync(requirementPath, `${JSON.stringify(requirements, null, 2)}\n`);
        const filename = filenameFor(packageJSON.name, updateVersion);
        const updateTarball = join(targetDir, filename);
        execFileSync('tar', ['-czf', updateTarball, '-C', unpacked, 'package']);
        copyFileSync(updateTarball, join(registryDir, filename));
        manifest.push({ name: packageJSON.name, version: updateVersion, tarball: filename, sha256: sha256(updateTarball) });
        generatedFiles.push(filename);
      } finally {
        rmSync(unpacked, { recursive: true, force: true });
      }
    } else {
      copyFileSync(source, join(targetDir, filename));
      manifest.push({ name: packageJSON.name, version: packageJSON.version, tarball: filename, sha256: sha256(source) });
    }
  }

  for (const packageName of versionedPackages) {
    if (packageName === failingServicePackage || (packageName === requiredConfigPackage && requiredConfig)) continue;
    const original = originals.get(packageName);
    if (!original) throw new Error(`journey update package is not packed: ${packageName}`);
    const unpacked = mkdtempSync('/tmp/kb-e2e-update-package-');
    try {
      execFileSync('tar', ['-xzf', original.source, '-C', unpacked]);
      const packageJSONPath = join(unpacked, 'package', 'package.json');
      const packageManifest = JSON.parse(readFileSync(packageJSONPath, 'utf8'));
      packageManifest.version = updateVersion;
      writeFileSync(packageJSONPath, `${JSON.stringify(packageManifest, null, 2)}\n`);
      const filename = basename(original.filename).replace(`-${original.packageJSON.version}.tgz`, `-${updateVersion}.tgz`);
      const updateTarball = join(targetDir, filename);
      execFileSync('tar', ['-czf', updateTarball, '-C', unpacked, 'package']);
      copyFileSync(updateTarball, join(registryDir, filename));
      manifest.push({ name: packageName, version: updateVersion, tarball: filename, sha256: sha256(updateTarball) });
      generatedFiles.push(filename);
    } finally {
      rmSync(unpacked, { recursive: true, force: true });
    }
  }
  writeFileSync(join(targetDir, 'manifest.json'), `${JSON.stringify(manifest, null, 2)}\n`);
  writeFileSync(join(targetDir, 'generated-packages.txt'), `${generatedFiles.join('\n')}\n`);
  return targetDir;
}

function filenameFor(name, version) {
  return `${name.replace(/^@/, '').replaceAll('/', '-')}-${version}.tgz`;
}


function sha256(path) {
  return createHash('sha256').update(readFileSync(path)).digest('hex');
}
