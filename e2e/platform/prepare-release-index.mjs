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
const binaryManifest = resolve(required('--binary-manifest'));
const binaryManifestTool = resolve(required('--binary-manifest-tool'));
const binaryChecksums = resolve(required('--binary-checksums'));
const binaryVersion = required('--binary-version');
const binaryRepository = value('--binary-repository') ?? 'kb-labs-team/kb-labs';
const binaryReleaseTag = value('--binary-release-tag') ?? 'journey-candidate';
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
const jumpVersion = `${match[1]}.${match[2]}.${Number(match[3]) + 2}`;
const minorVersion = `${match[1]}.${Number(match[2]) + 1}.0`;
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

// A direct N-to-N+2 candidate proves that the resolver accepts a matrix-valid
// jump without requiring an intermediate update invocation.
const jumpPackagesDir = join(resolve(output, '..'), 'jump-packages');
prepareJourneyUpdatePackages(stage, jumpPackagesDir, jumpVersion, { publishDir: packagesDir });
const jumpOutput = join(resolve(output, '..'), 'release-index-update-jump.json');
const jump = spawnSync(process.execPath, [
  prepareScript,
  '--flow', 'platform',
  '--channel', 'stable',
  '--artifacts-dir', jumpPackagesDir,
  '--binary-manifest', binaryManifest,
  '--platform-version', jumpVersion,
  '--platform-requires', platformRequires,
  '--platform-adapter-config', adapterConfig,
  '--platform-adapter-options', adapterOptions,
  '--platform-member-packages', platformMembers,
  '--adapter-overrides', adapterOverrides,
  '--registry', 'http://verdaccio:4873',
  '--output', jumpOutput,
  '--sealer-bin', sealer,
], { stdio: 'inherit' });
if (jump.status !== 0) process.exit(jump.status ?? 1);

const minorPackagesDir = join(resolve(output, '..'), 'minor-packages');
prepareJourneyUpdatePackages(stage, minorPackagesDir, minorVersion, { publishDir: packagesDir });
const minorOutput = join(resolve(output, '..'), 'release-index-update-minor.json');
const minor = spawnSync(process.execPath, [
  prepareScript, '--flow', 'platform', '--channel', 'stable',
  '--artifacts-dir', minorPackagesDir, '--binary-manifest', binaryManifest,
  '--platform-version', minorVersion, '--platform-requires', platformRequires,
  '--platform-adapter-config', adapterConfig, '--platform-adapter-options', adapterOptions,
  '--platform-member-packages', platformMembers, '--adapter-overrides', adapterOverrides,
  '--registry', 'http://verdaccio:4873', '--output', minorOutput, '--sealer-bin', sealer,
], { stdio: 'inherit' });
if (minor.status !== 0) process.exit(minor.status ?? 1);

// This release has a real, sealed gateway artifact whose service entrypoint
// exits non-zero. It exercises recovery after switch and failed host health.
const failedHostVersion = `${match[1]}.${Number(match[2]) + 2}.0`;
const failedHostPackagesDir = join(resolve(output, '..'), 'failed-host-packages');
prepareJourneyUpdatePackages(stage, failedHostPackagesDir, failedHostVersion, {
  publishDir: packagesDir,
  failingServicePackage: '@kb-labs/gateway-app',
});
const failedHostOutput = join(resolve(output, '..'), 'release-index-update-failed-host.json');
const failedHost = spawnSync(process.execPath, [
  prepareScript,
  '--flow', 'platform',
  '--channel', 'stable',
  '--artifacts-dir', failedHostPackagesDir,
  '--binary-manifest', binaryManifest,
  '--platform-version', failedHostVersion,
  '--platform-requires', platformRequires,
  '--platform-adapter-config', adapterConfig,
  '--platform-adapter-options', adapterOptions,
  '--platform-member-packages', platformMembers,
  '--adapter-overrides', adapterOverrides,
  '--registry', 'http://verdaccio:4873',
  '--output', failedHostOutput,
  '--sealer-bin', sealer,
], { stdio: 'inherit' });
if (failedHost.status !== 0) process.exit(failedHost.status ?? 1);

// Keep the checksum valid inside the sealed index, then the candidate
// publisher corrupts the served copy at this isolated URL so download-time
// verification sees a real artifact mismatch.
execFileSync(process.execPath, [
  binaryManifestTool,
  '--checksums', binaryChecksums,
  '--repository', binaryRepository,
  '--release-tag', binaryReleaseTag,
  '--version', binaryVersion,
  '--asset-base-url', `${binaryBaseUrl}/checksum-corrupt`,
  '--output', binaryManifest,
]);
const badChecksumOutput = join(resolve(output, '..'), 'release-index-update-bad-checksum.json');
const badChecksum = spawnSync(process.execPath, [
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
  '--output', badChecksumOutput,
  '--sealer-bin', sealer,
], { stdio: 'inherit' });
if (badChecksum.status !== 0) process.exit(badChecksum.status ?? 1);
const corruptDirectory = join(resolve(output, '..'), 'checksum-corrupt');
mkdirSync(corruptDirectory, { recursive: true });
for (const filename of ['kb-create-linux-amd64', 'kb-create-linux-arm64']) {
  const corrupted = readFileSync(join(binaryDirectory, filename));
  writeFileSync(join(corruptDirectory, filename), Buffer.concat([corrupted, Buffer.from('checksum-corruption')]));
}

execFileSync(process.execPath, [
  binaryManifestTool,
  '--checksums', binaryChecksums,
  '--repository', binaryRepository,
  '--release-tag', binaryReleaseTag,
  '--version', binaryVersion,
  '--asset-base-url', binaryBaseUrl,
  '--output', binaryManifest,
]);

const stagedFailureCandidates = [
  {
    key: 'config',
    version: `${match[1]}.${Number(match[2]) + 1}.1`,
    requirement: { id: 'journey.update.requiredConfig', path: '/platform/journey/updateRequired', required: true,
      hint: 'Configure platform.journey.updateRequired before retrying the update.' },
  },
  {
    key: 'doctor',
    version: `${match[1]}.${Number(match[2]) + 1}.2`,
    requirement: { id: 'journey.update.requiredSecret', path: '/platform/journey/updateSecret', required: true,
      secret: true, env: 'KB_JOURNEY_UPDATE_REQUIRED_SECRET', services: ['gateway'],
      hint: 'Configure KB_JOURNEY_UPDATE_REQUIRED_SECRET before retrying the update.' },
  },
];
for (const candidate of stagedFailureCandidates) {
  const candidatePackages = join(resolve(output, '..'), `staged-${candidate.key}-packages`);
  prepareJourneyUpdatePackages(stage, candidatePackages, candidate.version, {
    publishDir: packagesDir,
    requiredConfigPackage: '@kb-labs/adapters-vibeproxy',
    requiredConfig: candidate.requirement,
  });
  const candidateOutput = join(resolve(output, '..'), `release-index-update-${candidate.key}-failure.json`);
  const sealed = spawnSync(process.execPath, [
    prepareScript, '--flow', 'platform', '--channel', 'stable', '--artifacts-dir', candidatePackages,
    '--binary-manifest', binaryManifest, '--platform-version', candidate.version,
    '--platform-requires', platformRequires, '--platform-adapter-config', adapterConfig,
    '--platform-adapter-options', adapterOptions, '--platform-member-packages', platformMembers,
    '--adapter-overrides', adapterOverrides, '--registry', 'http://verdaccio:4873',
    '--output', candidateOutput, '--sealer-bin', sealer,
  ], { stdio: 'inherit' });
  if (sealed.status !== 0) process.exit(sealed.status ?? 1);
}

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
