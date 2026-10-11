#!/usr/bin/env node

import { execFileSync } from 'node:child_process';
import { existsSync, mkdtempSync, readFileSync, readdirSync, rmSync } from 'node:fs';
import { basename, join } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';

export function buildCommand(packageName) {
  return `corepack pnpm -r --filter "${packageName}..." --workspace-concurrency=2 build`;
}

export function failureMessage(manifest, tarball, failure) {
  return `ERROR: ${manifest.name} (${basename(tarball)}) is missing packed ${failure.field} output ${failure.target}. ` +
    `Build it with: ${buildCommand(manifest.name)}`;
}

function matchesTarget(files, target) {
  const normalized = target.replace(/^\.\//, '').replace(/\/$/, '');
  if (normalized.includes('*') || normalized.includes('?') || normalized.includes('[')) {
    let expression = '^';
    for (let index = 0; index < normalized.length; index += 1) {
      const character = normalized[index];
      if (character === '*' && normalized[index + 1] === '*') {
        index += 1;
        if (normalized[index + 1] === '/') {
          index += 1;
          expression += '(?:.*/)?';
        } else {
          expression += '.*';
        }
      } else if (character === '*') expression += '[^/]*';
      else if (character === '?') expression += '[^/]';
      else if (character === '[') {
        const end = normalized.indexOf(']', index + 1);
        if (end < 0) expression += '\\[';
        else {
          expression += normalized.slice(index, end + 1);
          index = end;
        }
      } else expression += character.replace(/[|\\{}()[\]^$+?.]/g, '\\$&');
    }
    const matcher = new RegExp(`${expression}$`);
    return files.some((file) => matcher.test(file));
  }
  return files.some((file) => file === normalized || file.startsWith(`${normalized}/`));
}

function exportTargets(value, targets = []) {
  if (typeof value === 'string' && value.startsWith('./')) targets.push(value);
  else if (Array.isArray(value)) value.forEach((entry) => exportTargets(entry, targets));
  else if (value && typeof value === 'object') Object.values(value).forEach((entry) => exportTargets(entry, targets));
  return targets;
}

export function validatePackageContents(manifest, packedPaths) {
  const files = packedPaths.map((file) => file.startsWith('package/') ? file.slice('package/'.length) : file);
  const failures = [];
  const check = (field, target) => {
    if (typeof target === 'string' && target && !matchesTarget(files, target)) {
      failures.push({ field, target });
    }
  };

  // `files` is an npm inclusion filter; absent optional source files such as
  // LICENSE are legal. Validate its build-output entries, which are the paths
  // that can be missing because a package was not built.
  for (const pattern of Array.isArray(manifest.files) ? manifest.files : []) {
    if (/^(?:dist|build|lib|out)(?:\/|$)/.test(pattern)) check('files', pattern);
  }
  check('main', manifest.main);
  exportTargets(manifest.exports).forEach((target) => check('exports', target));
  if (manifest.name === '@kb-labs/studio-app') check('Studio', './dist/index.html');
  return failures;
}

function stableJson(value) {
  if (Array.isArray(value)) return value.map(stableJson);
  if (value && typeof value === 'object') {
    return Object.fromEntries(Object.keys(value).sort().map((key) => [key, stableJson(value[key])]));
  }
  return value;
}

export function validatePackedSource(sourcePackage, packedPackage, {
  sourcePackedPackage,
  workspacePackageNames,
  candidateNames,
  sourceManifest,
  packedManifest,
} = {}) {
  const failures = [];
  if (sourcePackage.name !== packedPackage.name) {
    failures.push(`package name is ${packedPackage.name}; workspace source is ${sourcePackage.name}`);
  }
  if (sourcePackage.version !== packedPackage.version) {
    failures.push(`package version is ${packedPackage.version}; workspace source is ${sourcePackage.version}`);
  }

  if (sourcePackedPackage
      && JSON.stringify(stableJson(sourcePackedPackage)) !== JSON.stringify(stableJson(packedPackage))) {
    failures.push('package.json differs from the package freshly packed from workspace source');
  }

  for (const field of ['dependencies', 'optionalDependencies', 'peerDependencies']) {
    for (const dependency of Object.keys(packedPackage[field] ?? {})) {
      if (workspacePackageNames.has(dependency) && !candidateNames.has(dependency)) {
        failures.push(`${field}.${dependency} is required from the workspace but missing from the candidate package set`);
      }
    }
  }

  if (sourceManifest !== undefined
      && (packedManifest === undefined
          || JSON.stringify(stableJson(sourceManifest)) !== JSON.stringify(stableJson(packedManifest)))) {
    failures.push('dist/manifest.json differs from the workspace build output');
  }
  return failures;
}

export async function inspectPackedFixture(sourceDirectory) {
  const temporaryDirectory = mkdtempSync(join(tmpdir(), 'kb-packed-fixture-'));
  try {
    let archive = sourceDirectory;
    if (!sourceDirectory.endsWith('.tgz')) {
      execFileSync('pnpm', ['pack', '--pack-destination', temporaryDirectory, '--silent'], {
        cwd: sourceDirectory,
        stdio: 'ignore',
      });
      const archives = readdirSync(temporaryDirectory).filter((filename) => filename.endsWith('.tgz'));
      if (archives.length !== 1) return { packageName: basename(sourceDirectory), kind: 'unknown', failures: [`pnpm pack produced ${archives.length} fixture tarballs`] };
      archive = join(temporaryDirectory, archives[0]);
    }
    const packageRoot = join(temporaryDirectory, 'package');
    execFileSync('tar', ['-xzf', archive, '-C', temporaryDirectory]);
    const packageJson = JSON.parse(readFileSync(join(packageRoot, 'package.json'), 'utf8'));
    let discovery;
    try {
      discovery = await import('../../core/discovery/dist/index.js');
    } catch (error) {
      return { packageName: basename(packageRoot), kind: 'unknown', failures: [
        `product discovery could not be loaded from the workspace build: ${error.message}; ` +
        'build it with: corepack pnpm -r --filter "@kb-labs/core-discovery..." --workspace-concurrency=2 build',
      ] };
    }
    const diagnostics = new discovery.DiagnosticCollector();
    const loaded = await discovery.loadManifestFile(packageRoot, diagnostics);
    if (loaded.kind !== 'manifest') {
      let platform;
      try {
        platform = await import('../../core/platform/dist/index.js');
      } catch (error) {
        return { packageName: packageJson.name, kind: 'unknown', failures: [
          `product adapter loader could not be loaded from the workspace build: ${error.message}; ` +
          'build it with: corepack pnpm -r --filter "@kb-labs/core-platform..." --workspace-concurrency=2 build',
        ] };
      }
      const adapter = await platform.loadAdapterManifest({ packageRoot });
      if (adapter.kind === 'adapter') {
        return { packageName: packageJson.name, kind: 'adapter', adapterManifest: adapter.manifest, failures: [] };
      }
      const reason = adapter.kind === 'failed'
        ? `product adapter loader rejected ${packageJson.name}: ${adapter.reason}`
        : `product loaders could not classify ${packageJson.name} as a plugin or adapter`;
      if (loaded.kind === 'failed') {
        return { packageName: packageJson.name, kind: 'invalid', failures: [
          `product discovery could not load the packed manifest: ${loaded.cause}; ${reason}`,
        ] };
      }
      return { packageName: packageJson.name, kind: 'unknown', failures: [reason] };
    }
    const failures = [];
    for (const command of loaded.loaded.manifest.cli?.commands ?? []) {
      const handler = discovery.resolveCliHandlerPath(packageRoot, command.handler);
      if (!existsSync(handler)) failures.push(`${packageJson.name}: product discovery cannot resolve handler ${command.handler} to ${handler}`);
    }
    return { packageName: packageJson.name, kind: 'plugin', failures };
  } finally {
    rmSync(temporaryDirectory, { recursive: true, force: true });
  }
}

export function validateTarball(tarball) {
  const packageJson = execFileSync('tar', ['-xOzf', tarball, 'package/package.json'], { encoding: 'utf8' });
  const manifest = JSON.parse(packageJson);
  const packedPaths = execFileSync('tar', ['-tzf', tarball], { encoding: 'utf8' }).split(/\r?\n/).filter(Boolean);
  return { manifest, packedPaths, failures: validatePackageContents(manifest, packedPaths) };
}

function workspacePackages(workspaceRoot) {
  const output = execFileSync('pnpm', ['-r', 'list', '--depth', '-1', '--json'], {
    cwd: workspaceRoot,
    encoding: 'utf8',
  });
  const packages = new Map();
  for (const workspacePackage of JSON.parse(output)) {
    if (typeof workspacePackage.name !== 'string' || typeof workspacePackage.path !== 'string') continue;
    const manifest = JSON.parse(readFileSync(join(workspacePackage.path, 'package.json'), 'utf8'));
    if (manifest.name !== workspacePackage.name) {
      throw new Error(`workspace package identity changed while validating ${workspacePackage.path}`);
    }
    if (packages.has(manifest.name)) throw new Error(`duplicate workspace package name ${manifest.name}`);
    packages.set(manifest.name, { directory: workspacePackage.path, manifest });
  }
  const extras = JSON.parse(readFileSync(join(workspaceRoot, 'e2e/scripts/pack-extras.json'), 'utf8'));
  for (const extra of extras) {
    const directory = join(workspaceRoot, extra.directory);
    const manifest = JSON.parse(readFileSync(join(directory, 'package.json'), 'utf8'));
    if (packages.has(manifest.name)) throw new Error(`duplicate candidate package name ${manifest.name}`);
    packages.set(manifest.name, { directory, manifest });
  }
  return packages;
}

function packWorkspaceSource(directory) {
  const temporaryDirectory = mkdtempSync(join(tmpdir(), 'kb-candidate-source-'));
  try {
    execFileSync('pnpm', ['pack', '--pack-destination', temporaryDirectory, '--silent'], {
      cwd: directory,
      stdio: 'ignore',
    });
    const archives = readdirSync(temporaryDirectory).filter((filename) => filename.endsWith('.tgz'));
    if (archives.length !== 1) throw new Error(`pnpm pack produced ${archives.length} source tarballs for ${directory}`);
    const sourceArchive = join(temporaryDirectory, archives[0]);
    const sourceManifest = JSON.parse(execFileSync('tar', [
      '-xOzf', sourceArchive, 'package/package.json',
    ], { encoding: 'utf8' }));
    return sourceManifest;
  } finally {
    rmSync(temporaryDirectory, { recursive: true, force: true });
  }
}

export async function validateTarballAgainstWorkspace(tarball, packages, workspacePackageNames, candidateNames, fixtureNames) {
  const { manifest, packedPaths, failures } = validateTarball(tarball);
  const source = packages.get(manifest.name);
  if (!source) {
    failures.push(`package ${manifest.name} is not registered in the workspace; run "KB_JOURNEY_PACKAGES_READY=0 e2e/journey/prepare-candidate.sh" to repack workspace packages`);
    return { manifest, failures };
  }

  const readPacked = (relativePath) => {
    const archivePath = `package/${relativePath.replace(/^\.\//, '')}`;
    if (!packedPaths.includes(archivePath)) return undefined;
    return execFileSync('tar', ['-xOzf', tarball, archivePath], { encoding: 'utf8' });
  };
  const sourcePackedManifest = packWorkspaceSource(source.directory);
  const sourceManifestPath = source.manifest.kb?.manifest;
  if (typeof sourceManifestPath === 'string') {
    const relativePath = sourceManifestPath.replace(/^\.\//, '');
    const sourcePath = join(source.directory, relativePath);
    const packedText = readPacked(relativePath);
    const sourceExists = existsSync(sourcePath);
    const packedExists = packedText !== undefined;
    if (sourceExists && packedExists && packedText !== readFileSync(sourcePath, 'utf8')) {
      failures.push(`${relativePath} differs from the workspace build output`);
    } else if (sourceExists !== packedExists) {
      failures.push(`declared manifest output ${relativePath} is present in only the source or tarball`);
    }
  }

  const sourceManifestJsonPath = join(source.directory, 'dist', 'manifest.json');
  let sourceManifestJson;
  let packedManifestJson;
  if (existsSync(sourceManifestJsonPath)) {
    sourceManifestJson = JSON.parse(readFileSync(sourceManifestJsonPath, 'utf8'));
    const packedJson = readPacked('dist/manifest.json');
    if (packedJson !== undefined) packedManifestJson = JSON.parse(packedJson);
  }
  failures.push(...validatePackedSource(source.manifest, manifest, {
    sourcePackedPackage: sourcePackedManifest,
    workspacePackageNames,
    candidateNames,
    sourceManifest: sourceManifestJson,
    packedManifest: packedManifestJson,
  }));
  if (fixtureNames.has(manifest.name)) {
    const fixture = await inspectPackedFixture(tarball);
    failures.push(...fixture.failures);
  }
  return { manifest, failures };
}

async function main() {
  const directory = process.argv[2];
  const workspaceRoot = process.argv[3];
  if (!directory || !workspaceRoot) {
    process.stderr.write('Usage: validate-package-outputs.mjs <package-directory> <workspace-root>\n');
    return 2;
  }
  const tarballs = readdirSync(directory).filter((filename) => filename.endsWith('.tgz'))
    .map((filename) => join(directory, filename)).sort();
  if (tarballs.length === 0) {
    process.stderr.write(`ERROR: no package tarballs found in ${directory}\n`);
    return 1;
  }
  const packages = workspacePackages(workspaceRoot);
  const extras = JSON.parse(readFileSync(join(workspaceRoot, 'e2e/scripts/pack-extras.json'), 'utf8'));
  const fixtureNames = new Set(extras.map((extra) => JSON.parse(readFileSync(
    join(workspaceRoot, extra.directory, 'package.json'), 'utf8')).name));
  const workspacePackageNames = new Set(packages.keys());
  const candidateNames = new Set(tarballs.map((tarball) => validateTarball(tarball).manifest.name));
  let invalid = 0;
  for (const tarball of tarballs) {
    const { manifest, failures } = await validateTarballAgainstWorkspace(
      tarball, packages, workspacePackageNames, candidateNames, fixtureNames,
    );
    for (const failure of failures) {
      invalid += 1;
      const detail = typeof failure === 'string' ? failure : failureMessage(manifest, tarball, failure);
      process.stderr.write(
        `ERROR: ${manifest.name} (${basename(tarball)}) ${detail}. ` +
        'Repack from this workspace with: KB_JOURNEY_PACKAGES_READY=0 e2e/journey/prepare-candidate.sh\n',
      );
    }
  }
  if (invalid > 0) return 1;
  process.stdout.write(`Validated declared outputs and source fidelity in ${tarballs.length} package tarballs\n`);
  return 0;
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  main().then((code) => { process.exitCode = code; }).catch((error) => {
    process.stderr.write(`ERROR: package output validation failed: ${error.message}\n`);
    process.exitCode = 1;
  });
}
