#!/usr/bin/env node

// Build the E2E platform's V2 release index from the exact tarballs that are
// copied into the image. The registry manifest remains the E2E composition
// source, while the sealed index is the only input consumed by kb-create.

import { createHash } from 'node:crypto';
import { execFileSync, spawnSync } from 'node:child_process';
import { copyFileSync, mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { prepareJourneyUpdatePackages } from './journey-update-packages.mjs';
import { stablePlatformVersion } from './release-index-version.mjs';
import { buildBinaryManifest } from './binary-manifest.mjs';
import { readPlatformReleaseConfig } from '../../tools/kb-create/scripts/platform-release-config.mjs';

const args = process.argv.slice(2);
const value = name => {
  const index = args.indexOf(name);
  return index >= 0 ? args[index + 1] : undefined;
};
const required = name => {
  const result = value(name);
  if (!result) throw new Error(`${name} is required`);
  return result;
};

const packagesDir = resolve(required('--packages-dir'));
const registryManifest = JSON.parse(readFileSync(resolve(required('--registry-manifest')), 'utf8'));
const output = resolve(required('--output'));
const sealer = resolve(required('--sealer'));
const binaryDirectory = resolve(required('--binary-dir'));
const binaryBaseUrl = value('--binary-base-url') ?? 'http://origin:8080/journey-candidate';
const prepareScript = resolve(value('--prepare-script') ?? '/src/kb-create/scripts/prepare-release-index.mjs');
const releaseConfigPath = resolve(value('--release-config') ?? '.kb/kb.config.jsonc');
const stage = resolve(process.env.KB_JOURNEY_RELEASE_STAGE ?? '/tmp/kb-e2e-release-stage');
rmSync(stage, { recursive: true, force: true });
mkdirSync(stage, { recursive: true });

const wanted = [
  ...(registryManifest.core ?? []).map(item => item.name),
  ...(registryManifest.services ?? []).map(item => item.pkg),
  ...(registryManifest.plugins ?? []).map(item => item.pkg),
  ...(registryManifest.adapters ?? []).map(item => item.name),
].filter(Boolean);
const wantedSet = new Set(wanted);
const packages = new Map();

for (const filename of readdirSync(packagesDir).filter(name => name.endsWith('.tgz'))) {
  const tarball = join(packagesDir, filename);
  const packageJSON = JSON.parse(execFileSync('tar', ['-xOzf', tarball, 'package/package.json'], { encoding: 'utf8' }));
  if (wantedSet.has(packageJSON.name)) {
    copyFileSync(tarball, join(stage, filename));
    packages.set(packageJSON.name, { name: packageJSON.name, version: packageJSON.version, tarball: filename, sha256: sha256(tarball) });
  }
}

for (const name of wanted) {
  if (!packages.has(name)) throw new Error(`registry manifest package is not packed: ${name}`);
}

writeFileSync(join(stage, 'manifest.json'), `${JSON.stringify([...packages.values()], null, 2)}\n`);
const binaryManifest = join(stage, 'binary-manifest.json');
writeFileSync(binaryManifest, `${JSON.stringify(buildBinaryManifest(binaryDirectory, binaryBaseUrl), null, 2)}\n`);

const releaseConfig = readPlatformReleaseConfig(releaseConfigPath);
const adapterConfig = JSON.stringify(releaseConfig.adapters);
const adapterOptions = JSON.stringify({
  ...releaseConfig.adapterOptions,
  serviceTransport: {
    ...releaseConfig.adapterOptions.serviceTransport,
    services: Object.fromEntries((registryManifest.services ?? [])
      .filter(({ id, port }) => id && port)
      .map(({ id, port }) => [id, { url: `http://127.0.0.1:${port}` }])),
  },
});
const platformRequires = releaseConfig.platformRequires.join(',');
// Core plugins (`default: true`: workflow, marketplace, scaffold) ship as
// platform members, as release.flows.platform.platformMembers does for the
// real release. Every other plugin comes from the marketplace.
const platformMembers = releaseConfig.platformMembers.join(',');
const adapterOverrides = JSON.stringify({
  '@kb-labs/adapters-service-transport-http': {
    schema: 'kb.adapter/1',
    id: 'service-transport-http',
    implements: 'IServiceTransport',
  },
});
const result = spawnSync(process.execPath, [
  prepareScript,
  '--flow', 'platform',
  '--channel', 'stable',
  '--artifacts-dir', stage,
  '--binary-manifest', binaryManifest,
  // The platform composition under test needs a log store: workflow run logs
  // (GET /runs/:id/logs, the logs WS channel) are served by platform.logs,
  // which has no backend unless logRingBuffer or logPersistence is bound.
  // logRingBuffer is an extension adapter that taps the `logger` adapter
  // ("Extension logRingBuffer cannot connect: target adapter logger not
  // found" otherwise), so both must be required. Declaring requirements is
  // how the V2 resolver binds a capability (installed-but-unrequired
  // adapters are deliberately left unbound); .kb/overlays cannot rebind
  // platform adapters.
  '--platform-requires', platformRequires,
  '--platform-adapter-config', adapterConfig,
  '--platform-adapter-options', adapterOptions,
  '--platform-member-packages', platformMembers,
  '--adapter-overrides', adapterOverrides,
  '--registry', 'http://verdaccio:4873',
  '--output', output,
  '--sealer-bin', sealer,
], { stdio: 'inherit' });
if (result.status !== 0) process.exit(result.status ?? 1);

// The update journey needs a second sealed release. Give core-runtime and the
// SDK a journey-only patch package version while retaining their code, so the
// stable channel and package verification resolve one coherent N+1 release.
const baseVersion = stablePlatformVersion(JSON.parse(readFileSync(output, 'utf8')));
const match = /^(\d+)\.(\d+)\.(\d+)$/.exec(baseVersion ?? '');
if (!match) throw new Error(`platform release version is not a stable semantic version: ${baseVersion}`);
const updateVersion = `${match[1]}.${match[2]}.${Number(match[3]) + 1}`;
const updateOutput = join(resolve(output, '..'), 'release-index-update.json');
const updatePackagesDir = join(resolve(output, '..'), 'update-packages');
// Derive from the sealed base stage (exactly the wanted packages, one version
// each); the registry publisher reads packagesDir.
prepareJourneyUpdatePackages(stage, updatePackagesDir, updateVersion, { publishDir: packagesDir });
const update = spawnSync(process.execPath, [
  prepareScript,
  '--flow', 'platform',
  '--channel', 'stable',
  '--artifacts-dir', updatePackagesDir,
  '--binary-manifest', binaryManifest,
  '--platform-version', updateVersion,
  '--platform-requires', platformRequires,
  '--platform-adapter-config', adapterConfig,
  '--platform-adapter-options', adapterOptions,
  '--platform-member-packages', platformMembers,
  '--adapter-overrides', adapterOverrides,
  '--registry', 'http://verdaccio:4873',
  '--output', updateOutput,
  '--sealer-bin', sealer,
], { stdio: 'inherit' });
if (update.status !== 0) process.exit(update.status ?? 1);

// Re-seal the same runtime and SDK tarballs under a canary prerelease version.
// This is an immutable channel entry with a visible version marker, not a
// rewrite of the installed platform or a resolver shortcut.
const canaryVersion = `${match[1]}.${match[2]}.${Number(match[3]) + 2}-canary.1`;
const canaryPackagesDir = join(resolve(output, '..'), 'canary-packages');
prepareJourneyUpdatePackages(stage, canaryPackagesDir, canaryVersion, { publishDir: packagesDir });
const canaryOutput = join(resolve(output, '..'), 'release-index-canary.json');
const canary = spawnSync(process.execPath, [
  prepareScript,
  '--flow', 'platform',
  '--channel', 'canary',
  '--artifacts-dir', canaryPackagesDir,
  '--binary-manifest', binaryManifest,
  '--platform-version', canaryVersion,
  '--platform-requires', platformRequires,
  '--platform-adapter-config', adapterConfig,
  '--platform-adapter-options', adapterOptions,
  '--platform-member-packages', platformMembers,
  '--adapter-overrides', adapterOverrides,
  '--registry', 'http://verdaccio:4873',
  '--output', canaryOutput,
  '--sealer-bin', sealer,
], { stdio: 'inherit' });
if (canary.status !== 0) process.exit(canary.status ?? 1);

function sha256(path) {
  return createHash('sha256').update(readFileSync(path)).digest('hex');
}
