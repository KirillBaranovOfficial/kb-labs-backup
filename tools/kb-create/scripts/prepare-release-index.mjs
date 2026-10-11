#!/usr/bin/env node

// Publish-time preparation for the V2 release contract. This script owns the
// adapter from staged package manifests to the V2 index; the launcher only
// consumes the sealed result. It deliberately reads the exact tarballs from
// release stage instead of rebuilding or resolving npm tags.

import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';
import { builtinModules } from 'node:module';

const args = process.argv.slice(2);
const value = name => {
  const index = args.indexOf(name);
  return index >= 0 ? args[index + 1] : undefined;
};
const values = name => args.flatMap((argument, index) => argument === name ? [args[index + 1]] : []);
const required = name => {
  const result = value(name);
  if (!result) throw new Error(`${name} is required`);
  return result;
};

const artifactsDir = resolve(required('--artifacts-dir'));
const sdkArtifactsDir = value('--sdk-artifacts-dir') ? resolve(value('--sdk-artifacts-dir')) : undefined;
const output = resolve(required('--output'));
const flow = required('--flow');
const channel = value('--channel') ?? 'stable';
const registry = value('--registry') ?? 'https://registry.npmjs.org';
const platformPackage = value('--platform-package') ?? '@kb-labs/core-runtime';
const sdkPackage = value('--sdk-package') ?? '@kb-labs/sdk';
const binaryManifestPath = value('--binary-manifest') ? resolve(value('--binary-manifest')) : undefined;
if (flow === 'platform' && !binaryManifestPath) throw new Error('--binary-manifest is required for the unified platform release-index');
const minLauncherVersion = value('--min-launcher-version');
const releaseNotes = value('--release-notes');
const sealerBin = value('--sealer-bin') ? resolve(value('--sealer-bin')) : undefined;
const historyIndexes = values('--history-index').map(path => resolve(path));
const platformRequires = (value('--platform-requires') ?? '')
  .split(',')
  .map(item => item.trim())
  .filter(Boolean)
  .map(capability => ({ capability, requiredBy: 'platform' }));
const platformAdapterConfig = value('--platform-adapter-config')
  ? JSON.parse(value('--platform-adapter-config'))
  : undefined;
const platformAdapterOptions = value('--platform-adapter-options')
  ? JSON.parse(value('--platform-adapter-options'))
  : undefined;
const platformMemberPackages = (value('--platform-member-packages') ?? '')
  .split(',')
  .map(item => item.trim())
  .filter(Boolean);
// The platform runs as ONE supervised host process (ADR-0043/0045); the sealed
// index declares it, and the launcher has no other way to start a platform. The
// host package is a platform member, its executable comes from its own bin, and
// a recovery tool (kb-create doctor --recover) is declared only when it ships.
const hostPackage = value('--host-package') ?? '@kb-labs/host-app';
const hostHealthURL = value('--host-health-url') ?? 'http://127.0.0.1:4000/health';

const readStage = directory => {
  const entries = JSON.parse(readFileSync(join(directory, 'manifest.json'), 'utf8'));
  if (!Array.isArray(entries) || entries.length === 0) throw new Error(`stage manifest contains no artifacts: ${directory}`);
  return entries.map(item => ({ item, directory }));
};
const stagedEntries = [...readStage(artifactsDir), ...(sdkArtifactsDir ? readStage(sdkArtifactsDir) : [])];
const stage = stagedEntries.map(({ item }) => item);
if (new Set(stage.map(item => item.name)).size !== stage.length) throw new Error('stage manifests contain duplicate package names');

const byName = new Map(stage.map(item => [item.name, item]));
const sourceDirByName = new Map(stagedEntries.map(({ item, directory }) => [item.name, directory]));
const sizeBytesFor = item => statSync(resolve(sourceDirByName.get(item.name) ?? artifactsDir, item.tarball)).size;
const platform = byName.get(platformPackage);
if (!platform) throw new Error(`platform package ${platformPackage} is absent from stage manifest`);
const resolvedPlatformVersion = platform.version;

// The default adapter map is part of the sealed platform configuration, not a
// suggestion for an already-installed workspace.  Its packages must therefore
// travel with the platform members.  Resolve subpath exports (for example
// "@scope/adapter/kv") back to their staged package, and fail closed when the
// release plan did not produce that package.
const stagedPackageForAdapter = configuredPackage => {
  const matches = stage
    .filter(item => configuredPackage === item.name || configuredPackage.startsWith(`${item.name}/`))
    .sort((left, right) => right.name.length - left.name.length);
  if (matches.length === 0) {
    throw new Error(`configured platform adapter ${configuredPackage} is absent from stage manifest`);
  }
  return matches[0].name;
};
const configuredPlatformAdapterPackages = platformAdapterConfig
  ? [...new Set(Object.values(platformAdapterConfig).map(configuredPackage => {
      if (typeof configuredPackage !== 'string' || configuredPackage.length === 0) {
        throw new Error('platform adapter configuration must contain package strings');
      }
      return stagedPackageForAdapter(configuredPackage);
    }))]
  : [];
// Keep the bindings and options for precisely the required platform slots in
// the release index. This makes fresh-install defaults travel with the same
// capability list the resolver uses to produce provider bindings.
const requiredAdapterSlots = new Set(platformRequires.map(({ capability }) => capability));
const portablePlatformAdapterConfig = platformAdapterConfig
  ? Object.fromEntries(Object.entries(platformAdapterConfig).filter(([slot]) => requiredAdapterSlots.has(slot)))
  : undefined;
const portablePlatformAdapterOptions = platformAdapterOptions
  ? Object.fromEntries(Object.entries(platformAdapterOptions).filter(([slot]) => requiredAdapterSlots.has(slot)))
  : undefined;
for (const packageName of platformMemberPackages) {
  if (!byName.has(packageName)) {
    throw new Error(`required platform member ${packageName} is absent from stage manifest`);
  }
}
const sdk = byName.get(sdkPackage);
if (!sdk) throw new Error(`SDK package ${sdkPackage} is absent from stage manifest; compatibility cannot be validated without an exact SDK artifact`);
const binaries = binaryManifestPath ? JSON.parse(readFileSync(binaryManifestPath, 'utf8')).binaries : [];
if (!Array.isArray(binaries)) throw new Error('binary manifest must contain a binaries array');
for (const binary of binaries) {
  for (const field of ['id', 'version', 'os', 'arch', 'url', 'filename', 'sha256']) if (!binary[field]) throw new Error(`binary manifest entry is missing ${field}`);
}
const staging = resolve(`${artifactsDir}/v2-manifest-root`);
rmSync(staging, { recursive: true, force: true });
mkdirSync(staging, { recursive: true });

const manifestFor = item => {
  const packageDir = join(staging, 'node_modules', item.name);
  mkdirSync(packageDir, { recursive: true });
  const tarball = resolve(sourceDirByName.get(item.name) ?? artifactsDir, item.tarball);
  if (!existsSync(tarball)) throw new Error(`staged tarball is missing: ${tarball}`);
  const result = spawnSync('tar', ['-xzf', tarball, '-C', packageDir, '--strip-components=1'], { encoding: 'utf8' });
  if (result.status !== 0) throw new Error(`extract ${item.name}: ${result.stderr || result.stdout}`);
  const artifact = join(packageDir, 'dist', 'manifest.json');
  if (existsSync(artifact)) {
    const manifest = JSON.parse(readFileSync(artifact, 'utf8'));
    if (!['kb.service/1', 'kb.adapter/1', 'kb.plugin/3'].includes(manifest?.schema)) {
      throw new Error(`${item.name} dist/manifest.json uses unsupported schema ${manifest?.schema ?? '(missing)'}`);
    }
    return manifest;
  }
  const packageJSON = JSON.parse(readFileSync(join(packageDir, 'package.json'), 'utf8'));
  const declaredManifest = packageJSON.kb?.manifest;
  if (declaredManifest) throw new Error(`${item.name} declares kb.manifest but its build did not emit dist/manifest.json; rebuild the package with @kb-labs/devkit/tsup/node`);
  return undefined;
};

// A package declares the configuration it needs (non-secret paths, secrets and
// the environment variables/services they feed) in a `kb-create.requirements.json`
// shipped in its tarball. It is separate from the declarative dist/manifest.json
// artifact: this file only adds requirements and never replaces the package's
// service, plugin, or adapter contract. The sealer
// validates each requirement's shape; here we only fail closed on a malformed
// file, so a typo cannot silently ship a package without its requirements.
const REQUIREMENTS_FILE = 'kb-create.requirements.json';
const REQUIREMENTS_SCHEMA = 'kb.create.requirements/v1';
const requirementsFor = item => {
  const packageDir = join(staging, 'node_modules', item.name);
  for (const candidate of [join(packageDir, REQUIREMENTS_FILE), join(packageDir, 'dist', REQUIREMENTS_FILE)]) {
    if (!existsSync(candidate)) continue;
    let parsed;
    try {
      parsed = JSON.parse(readFileSync(candidate, 'utf8'));
    } catch (error) {
      throw new Error(`${item.name} ships an unreadable ${REQUIREMENTS_FILE}: ${error.message}`);
    }
    if (parsed?.schema !== REQUIREMENTS_SCHEMA || !Array.isArray(parsed.requirements)) {
      throw new Error(`${item.name} ships an invalid ${REQUIREMENTS_FILE}: expected schema ${REQUIREMENTS_SCHEMA} and a requirements array`);
    }
    return parsed.requirements;
  }
  return [];
};
const uniqueRequirements = (packageName, list) => {
  const seen = new Set();
  for (const requirement of list) {
    if (typeof requirement?.id !== 'string' || requirement.id === '') {
      throw new Error(`${packageName} declares a configuration requirement without an id`);
    }
    if (seen.has(requirement.id)) {
      throw new Error(`${packageName} declares configuration requirement ${requirement.id} more than once`);
    }
    seen.add(requirement.id);
  }
  return list;
};

const packageJSONFor = item => {
  const path = join(staging, 'node_modules', item.name, 'package.json');
  if (!existsSync(path)) throw new Error(`${item.name} tarball has no package.json`);
  return JSON.parse(readFileSync(path, 'utf8'));
};

const tarballURL = item => {
  const packageBase = item.name.split('/').pop();
  return `${registry.replace(/\/$/, '')}/${item.name}/-/${packageBase}-${item.version}.tgz`;
};
const component = (item, manifest, id = item.name.split('/').pop().replace(/-entry$/, '')) => ({
  id,
  version: item.version,
  package: item.name,
  tarball: tarballURL(item),
  sha256: item.sha256,
  sizeBytes: sizeBytesFor(item),
  ...(manifest?.compatibility?.platformRange ? { platformRange: manifest.compatibility.platformRange } : {}),
  ...(manifest?.compatibility?.sdkRange ? { sdkRange: manifest.compatibility.sdkRange } : {}),
  config: manifest?.requirements ?? [],
});

const services = [];
const plugins = [];
const adapters = [];
// Single source of truth for each staged package's normalized catalog ID.
// The platform `members[]` array (built below, after this loop) MUST reuse
// these exact same IDs rather than re-deriving them from packageName via
// idFor()/-entry stripping — a plugin's own kb.plugin/3 manifest is the
// source of truth for its ID (see pluginIdFor's doc comment), and package
// names don't reliably follow that id (e.g. @kb-labs/release-manager-cli's
// manifest declares id "@kb-labs/release"). Re-deriving IDs independently
// for members caused the release index to write "release-manager-cli" as
// that member's id while the package's own kb-create.manifest.json (written
// below using normalizedID) shipped "release" — a divergence that failed
// installed.Load()'s id equality check at publish time.
const normalizedIdByPackage = new Map();
for (const item of stage) {
  const manifest = manifestFor(item);
  const normalizedID = item.name === platformPackage
    ? 'platform'
    : item.name === sdkPackage
      ? 'sdk'
      : manifest?.schema === 'kb.service/1'
        ? manifest.id
        : manifest?.schema === 'kb.adapter/1'
          ? manifest.id
        : manifest?.schema === 'kb.plugin/3'
          ? pluginIdFor(manifest)
        : idFor(item.name);
  normalizedIdByPackage.set(item.name, normalizedID);
  const generatedRequirements = item.name === platformPackage
    ? [
        ...(portablePlatformAdapterConfig ? [{ id: 'platform.adapters', path: '/platform/adapters', default: portablePlatformAdapterConfig }] : []),
        ...(portablePlatformAdapterOptions ? [{ id: 'platform.adapterOptions', path: '/platform/adapterOptions', default: portablePlatformAdapterOptions }] : []),
      ]
    : [];
  writeFileSync(join(staging, 'node_modules', item.name, 'kb-create.manifest.json'), `${JSON.stringify({
    schema: 'kb.create.artifact-manifest/v2',
    id: normalizedID,
    package: item.name,
    version: item.version,
    requirements: uniqueRequirements(item.name, [
      ...(manifest?.schema === 'kb.create.artifact-manifest/v2' ? manifest.requirements ?? [] : []),
      ...requirementsFor(item),
      ...generatedRequirements,
    ]),
  })}\n`);
  if (!manifest) continue;
  if (manifest.schema === 'kb.service/1') {
    const packageJSON = packageJSONFor(item);
    services.push({
      id: manifest.id,
      packageName: item.name,
      command: manifest.bin ? Object.keys(manifest.bin)[0] : Object.keys(packageJSON.bin ?? {})[0] ?? item.name.split('/').pop(),
      port: manifest.runtime?.port ?? 0,
      healthCheck: manifest.runtime?.healthCheck ?? '',
      dependsOn: manifest.dependsOn ?? [],
      required: true,
    });
  } else if (manifest.schema === 'kb.plugin/3') {
    const pluginId = pluginIdFor(manifest, item.name);
    const requires = (manifest.platform?.requires ?? []).map(capability => ({ capability, requiredBy: pluginId }));
    plugins.push({ ...component(item, manifest, pluginId), requires });
  } else if (manifest.schema === 'kb.adapter/1') {
    const capabilities = (Array.isArray(manifest.implements) ? manifest.implements : [manifest.implements])
      .filter(Boolean)
      .map(interfaceToCapability);
    if (manifest.defaultFor !== undefined && !Array.isArray(manifest.defaultFor)) {
      throw new Error(`${item.name} defaultFor must be an array of provided capabilities`);
    }
    const defaults = manifest.defaultFor ?? [];
    for (const capability of defaults) {
      if (!capabilities.includes(capability)) {
        throw new Error(`${item.name} declares defaultFor ${capability} but does not implement it`);
      }
    }
    adapters.push({
      ...component(item, manifest, manifest.id ?? idFor(item.name)),
      provides: capabilities,
      ...(defaults.length ? { defaultFor: defaults } : {}),
    });
  }
}

const sdkPackageJSON = packageJSONFor(sdk);
const sdkPlatformRange = sdkPackageJSON.peerDependencies?.[platformPackage];
if (!sdkPlatformRange) throw new Error(`${sdkPackage} does not declare a peer dependency on ${platformPackage}`);

// Platform members and the SDK share one installed package graph. A runtime
// peer imported by any of them must therefore be included as a platform member
// so kb-create pins one copy for every plugin. Validate all published JS imports
// against exact package metadata while deriving this closure.
const platformMemberRoots = new Set([
  ...services.map(service => service.packageName),
  hostPackage,
  ...platformMemberPackages,
  ...configuredPlatformAdapterPackages,
  sdk.name,
]);
const packageRootFor = specifier => specifier.startsWith('@')
  ? specifier.split('/').slice(0, 2).join('/')
  : specifier.split('/')[0];
const builtinPackageNames = new Set(builtinModules.flatMap(name => [name, name.replace(/^node:/, '')]));
const runtimeImportsFor = item => {
  const dist = join(staging, 'node_modules', item.name, 'dist');
  if (!existsSync(dist)) return [];
  const allFiles = [];
  const visit = directory => {
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
      const path = join(directory, entry.name);
      if (entry.isDirectory()) visit(path);
      else if (/\.(?:c|m)?js$/.test(entry.name)) allFiles.push(path);
    }
  };
  visit(dist);
  const packageJSON = packageJSONFor(item);
  const nonRuntimeEntrypoints = packageJSON.kbLabs?.release?.nonRuntimeEntrypoints;
  let files = allFiles;
  if (nonRuntimeEntrypoints !== undefined) {
    if (!Array.isArray(nonRuntimeEntrypoints) || nonRuntimeEntrypoints.some(name => typeof name !== 'string')) {
      throw new Error(`${item.name} kbLabs.release.nonRuntimeEntrypoints must be an array of export names`);
    }
    const exports = packageJSON.exports;
    if (!exports || typeof exports !== 'object' || Array.isArray(exports)) {
      throw new Error(`${item.name} declares non-runtime entrypoints without an exports map`);
    }
    const unknownEntrypoints = nonRuntimeEntrypoints.filter(name => !Object.hasOwn(exports, name));
    if (unknownEntrypoints.length) {
      throw new Error(`${item.name} declares non-runtime entrypoints that are absent from exports: ${unknownEntrypoints.join(', ')}`);
    }
    const runtimeEntrypoints = Object.keys(exports).filter(name => !nonRuntimeEntrypoints.includes(name));
    const targetFor = value => {
      if (typeof value === 'string') return value;
      if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined;
      return targetFor(value.import ?? value.node ?? value.default);
    };
    files = runtimeEntrypoints.map(name => {
      const target = targetFor(exports[name]);
      if (!target || !target.startsWith('./dist/')) {
        throw new Error(`${item.name} runtime export ${name} must resolve to a JavaScript file under dist`);
      }
      const path = join(dirname(join(staging, 'node_modules', item.name, 'package.json')), target);
      if (!existsSync(path) || !/\.(?:c|m)?js$/.test(path)) {
        throw new Error(`${item.name} runtime export ${name} points to a missing JavaScript file: ${target}`);
      }
      return path;
    });
  }
  const imports = new Map();
  const visited = new Set();
  const patterns = [
    /^\s*(?:import|export)\s+[^;]*?\s+from\s*["']([^"']+)["']/gm,
    /^\s*import\s*["']([^"']+)["']/gm,
    /\brequire\s*\(\s*["']([^"']+)["']\s*\)/g,
  ];
  for (let index = 0; index < files.length; index += 1) {
    const file = files[index];
    if (visited.has(file)) continue;
    visited.add(file);
    const source = readFileSync(file, 'utf8');
    for (const pattern of patterns) {
      for (const match of source.matchAll(pattern)) {
        imports.set(match[1], { specifier: match[1], optional: false, static: true });
        if (match[1].startsWith('.')) {
          const base = join(dirname(file), match[1]);
          const relativeFile = [base, `${base}.js`, `${base}.mjs`, `${base}.cjs`, join(base, 'index.js')].find(existsSync);
          if (relativeFile && relativeFile.startsWith(dist)) files.push(relativeFile);
        }
      }
    }
    for (const match of source.matchAll(/\bimport\s*\(\s*["']([^"']+)["']\s*\)/g)) {
      const before = source.slice(Math.max(0, match.index - 400), match.index);
      const after = source.slice(match.index + match[0].length, match.index + match[0].length + 400);
      const optional = before.lastIndexOf('try {') > before.lastIndexOf('}') && /}\s*catch\s*(?:\([^)]*\))?\s*{/.test(after);
      imports.set(match[1], { specifier: match[1], optional, static: false });
    }
  }
  return [...imports.values()];
};
const dependencyClosureCache = new Map();
const dependencyClosureFor = packageName => {
  if (dependencyClosureCache.has(packageName)) return dependencyClosureCache.get(packageName);
  const closure = new Set([packageName]);
  dependencyClosureCache.set(packageName, closure);
  const item = byName.get(packageName);
  if (!item) return closure;
  const packageJSON = packageJSONFor(item);
  const dependencies = [
    ...Object.keys(packageJSON.dependencies ?? {}),
    ...Object.keys(packageJSON.optionalDependencies ?? {}),
  ];
  for (const dependency of dependencies) {
    if (!dependency.startsWith('@kb-labs/')) continue;
    for (const transitive of dependencyClosureFor(dependency)) closure.add(transitive);
  }
  return closure;
};
// Platform members and the SDK are installed into one platform node_modules
// tree. Derive peer availability from that exact sealed package graph; mere
// presence in the release stage does not make a peer available to consumers.
const platformProvidedPackages = new Set();
for (const packageName of platformMemberRoots) {
  for (const dependency of dependencyClosureFor(packageName)) {
    if (byName.has(dependency)) platformProvidedPackages.add(dependency);
  }
}
const packagesToValidate = [...platformProvidedPackages];
for (let index = 0; index < packagesToValidate.length; index += 1) {
  const packageName = packagesToValidate[index];
  const item = byName.get(packageName);
  // Preserve the host-specific validation below, which reports the release
  // contract violation with its actionable package role.
  if (!item && packageName === hostPackage) continue;
  if (!item) throw new Error(`platform package ${packageName} is absent from stage manifest`);
  const packageJSON = packageJSONFor(item);
  const dependencies = new Set([
    ...Object.keys(packageJSON.dependencies ?? {}),
    ...Object.keys(packageJSON.optionalDependencies ?? {}),
  ]);
  const dependencyClosure = dependencyClosureFor(packageName);
  const peers = new Set(Object.keys(packageJSON.peerDependencies ?? {}));
  const optionalPeers = new Set(Object.entries(packageJSON.peerDependenciesMeta ?? {})
    .filter(([, metadata]) => metadata?.optional === true)
    .map(([name]) => name));
  for (const { specifier, optional, static: staticImport } of runtimeImportsFor(item)) {
    if (specifier.startsWith('.') || specifier.startsWith('/') || specifier.startsWith('node:')) continue;
    const dependency = packageRootFor(specifier);
    if (builtinPackageNames.has(dependency)) continue;
    if (dependency === packageName) continue;
    // npm resolves external dependencies through each package's installed
    // dependency graph. This sealer only owns @kb-labs artifacts and peers.
    if (!dependency.startsWith('@kb-labs/')) continue;
    if (optionalPeers.has(dependency)) {
      if (staticImport) {
        throw new Error(`${packageName} runtime entrypoint ${specifier} statically imports optional peer ${dependency}`);
      }
      continue;
    }
    const providedPeer = peers.has(dependency) && platformProvidedPackages.has(dependency);
    if (peers.has(dependency) && !providedPeer) {
      throw new Error(`${packageName} runtime import ${specifier} is satisfied only by peer ${dependency}, which the platform package graph does not provide`);
    }
    const declaredByPackage = dependencies.has(dependency) || dependencyClosure.has(dependency) || providedPeer;
    if (optional && !declaredByPackage) continue;
    if (!declaredByPackage) {
      throw new Error(`${packageName} runtime import ${specifier} is neither a dependency nor a sealed peer`);
    }
  }
}

const hostItem = byName.get(hostPackage);
if (!hostItem) throw new Error(`host package ${hostPackage} is absent from stage manifest; a platform release must ship the host`);
const hostBins = packageJSONFor(hostItem).bin ?? {};
if (typeof hostBins['kb-host'] !== 'string') throw new Error(`${hostPackage} must declare a kb-host executable in its package.json bin`);
const host = {
  command: 'node_modules/.bin/kb-host',
  healthUrl: hostHealthURL,
  ...(typeof hostBins['kb-recover'] === 'string' ? { recover: { command: 'node_modules/.bin/kb-recover' } } : {}),
};

const exportValue = {
  channels: { [channel]: resolvedPlatformVersion },
  compatibility: {
    schema: 'kb.release-compatibility/2',
    labels: [
      { id: `platform@${resolvedPlatformVersion}`, kind: 'platform', artifactId: 'platform', version: resolvedPlatformVersion, requires: [{ label: `sdk@${sdk.version}`, constraint: sdkPlatformRange }], status: 'prepared', validatedBy: ['stage', 'package-manifest', 'artifact-hash', 'sdk-peer-dependency'] },
      { id: `sdk@${sdk.version}`, kind: 'sdk', artifactId: 'sdk', version: sdk.version, status: 'prepared', validatedBy: ['stage', 'package-manifest', 'artifact-hash', 'sdk-peer-dependency'] },
      ...binaries.map(binary => { const version = binary.version; return { id: `binary:${binary.id}@${version}:${binary.os}/${binary.arch}`, kind: 'binary', artifactId: binary.id, version, requires: [{ label: `platform@${resolvedPlatformVersion}` }, { label: `sdk@${sdk.version}` }], status: 'prepared', validatedBy: ['release-asset', 'artifact-hash', 'post-publish-smoke'] }; }),
    ],
  },
  platforms: [{
    id: 'platform',
    version: resolvedPlatformVersion,
    ...(releaseNotes !== undefined ? { releaseNotes } : {}),
    package: platform.name,
    tarball: tarballURL(platform),
    sha256: platform.sha256,
    sizeBytes: sizeBytesFor(platform),
    ...(minLauncherVersion ? { minLauncherVersion } : {}),
    requires: platformRequires,
    config: [
      ...(portablePlatformAdapterConfig ? [{
        id: 'platform.adapters',
        path: '/platform/adapters',
        default: JSON.stringify(portablePlatformAdapterConfig),
      }] : []),
      ...(portablePlatformAdapterOptions ? [{
        id: 'platform.adapterOptions',
        path: '/platform/adapterOptions',
        default: JSON.stringify(portablePlatformAdapterOptions),
      }] : []),
    ],
    host,
    profiles: {
      default: { platformVersion: platform.version, services: services.map(({ packageName, ...service }) => service) },
      client: { profile: 'client', platformVersion: platform.version, services: [] },
    },
    binaries,
    members: [...new Set([
      ...services.map(service => service.packageName),
      hostPackage,
      ...platformMemberPackages,
      ...configuredPlatformAdapterPackages,
    ])].map(packageName => {
      const item = byName.get(packageName);
      return item ? component(item, undefined,
        normalizedIdByPackage.get(packageName) ?? idFor(packageName),
      ) : undefined;
    }),
  }],
  sdks: sdk ? [component(sdk, undefined, 'sdk')] : [],
  plugins,
  adapters,
};

const exportPath = join(artifactsDir, 'manifest-export.json');
writeFileSync(exportPath, `${JSON.stringify(exportValue, null, 2)}\n`);

const sealer = sealerBin
  ? spawnSync(sealerBin, ['--input', exportPath, '--manifest-root', staging, '--output', output,
    ...historyIndexes.flatMap(index => ['--history-index', index])], { stdio: 'inherit' })
  : spawnSync('go', ['run', './v2/cmd/kb-create-release-index', '--input', exportPath, '--manifest-root', staging, '--output', output,
    ...historyIndexes.flatMap(index => ['--history-index', index])], {
      cwd: resolve(new URL('../', import.meta.url).pathname),
      stdio: 'inherit',
    });
if (sealer.status !== 0) process.exit(sealer.status ?? 1);

function idFor(packageName) {
  return packageName.split('/').pop().replace(/-entry$/, '');
}

// A plugin's kb.plugin/3 manifest is the source of truth for its ID, including
// its scope. Package names and release-index short names are not plugin IDs.
function pluginIdFor(manifest) {
  return manifest.id;
}

// Convert an adapter interface name (e.g. "IServiceTransport", "IKVStore",
// "ILLM") to the camelCase capability token the platform requires by (e.g.
// "serviceTransport", "kvStore", "llm"). Lowercasing every capital letter
// (the previous approach) collapses "IServiceTransport" to
// "servicetransport", which never matches the "serviceTransport" capability
// the platform declares — so no adapter is ever found and bootstrap fails
// with KB_INSTALL_PROVIDER_UNRESOLVED. Only the leading run of capitals needs
// lowercasing, and only up to (not including) the capital that starts the
// next word — "KVStore" -> "kvStore", not "kVStore" or "kvstore".
function interfaceToCapability(name) {
  const stripped = name.replace(/^I/, '');
  return stripped.replace(/^[A-Z]+/, (leading) => {
    if (leading.length <= 1 || leading.length === stripped.length) return leading.toLowerCase();
    return leading.slice(0, -1).toLowerCase() + leading.slice(-1);
  });
}

const digest = createHash('sha256').update(readFileSync(output)).digest('hex');
console.log(`Release index prepared: ${output}`);
console.log(`Release index bytes: ${digest}`);
