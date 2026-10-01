import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { copyFileSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { basename, join, resolve } from 'node:path';

const VERSIONED_PACKAGES = ['@kb-labs/core-runtime', '@kb-labs/sdk'];

// Build a release stage for a journey-only platform version from an existing
// stage: every package is reused unchanged except core-runtime and the SDK,
// which get the new version. The target holds the same manifest.json the
// release-index preparer reads; generated tarballs are also written to
// publishDir so the local registry can serve them.
export function prepareJourneyUpdatePackages(packagesDir, updatePackagesDir, updateVersion, { publishDir } = {}) {
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
    if (VERSIONED_PACKAGES.includes(packageJSON.name)) {
      originals.set(packageJSON.name, { source, filename, packageJSON });
    } else {
      copyFileSync(source, join(targetDir, filename));
      manifest.push({ name: packageJSON.name, version: packageJSON.version, tarball: filename, sha256: sha256(source) });
    }
  }

  for (const packageName of VERSIONED_PACKAGES) {
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

function sha256(path) {
  return createHash('sha256').update(readFileSync(path)).digest('hex');
}
