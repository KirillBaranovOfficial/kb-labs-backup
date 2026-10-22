import test from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import registryManifest from '../../../e2e/platform/registry-manifest.json' with { type: 'json' };
import {
  configuredAdapterPackages,
  platformPackageRoots,
  readPlatformReleaseConfig,
  shippedPlatformPackageSet,
} from './platform-release-config.mjs';

test('the platform release config binds every persistent built-in used by a fresh install', () => {
  const releaseConfigPath = fileURLToPath(new URL('../config/platform-release.jsonc', import.meta.url));
  const config = readPlatformReleaseConfig(releaseConfigPath);
  assert.deepEqual(config.marketplaceSources, [{ kind: 'npm', url: 'https://registry.npmjs.org' }]);
  assert.deepEqual(config.platformRequires, [...Object.keys(config.adapters), 'llm']);
  assert.ok(config.platformRequires.includes('llm'), 'the default install must include the implicit OpenAI provider');
  assert.equal(config.adapters.llm, undefined, 'llm remains selected by its adapter manifest default');
  assert.equal(config.adapterOptions.storage, undefined,
    'the filesystem adapter manifest owns the implicit per-project storage path');
  const developerConfigPath = fileURLToPath(new URL('../../../.kb/kb.config.jsonc', import.meta.url));
  const developerConfig = readPlatformReleaseConfig(developerConfigPath);
  assert.equal(developerConfig.adapterOptions.storage.baseDir, 'storage',
    'workspace development keeps its explicitly configured storage directory');
  for (const slot of Object.keys(config.adapters)) {
    assert.equal(typeof config.adapters[slot], 'string', `${slot} must have a fresh-install binding`);
    assert.ok(config.platformRequires.includes(slot), `${slot} must be required in the release index`);
  }
  assert.equal(config.adapters.documentDatabase, '@kb-labs/adapters-sqlite');
  assert.equal(config.adapters.eventBus, '@kb-labs/adapters-eventbus-cache');
  assert.equal(config.adapters.kvStore, '@kb-labs/adapters-sqlite/kv');
  assert.equal(config.adapterOptions.documentDatabase.filename, 'database/kb.sqlite');
  assert.equal(config.adapterOptions.kvStore.filename, 'database/kb.sqlite');
});

test('the shipped platform package set contains the runtime dependency closure without plugins', () => {
  const workspaceRoot = fileURLToPath(new URL('../../../', import.meta.url));
  const workspace = JSON.parse(execFileSync('corepack', ['pnpm', '-r', 'list', '--depth', '-1', '--json'], {
    cwd: workspaceRoot,
    encoding: 'utf8',
  }));
  const packageManifests = new Map(workspace.map(({ path }) => {
    const manifest = JSON.parse(readFileSync(join(path, 'package.json'), 'utf8'));
    return [manifest.name, manifest];
  }));
  const configPath = fileURLToPath(new URL('../config/platform-release.jsonc', import.meta.url));
  const config = readPlatformReleaseConfig(configPath);
  const roots = platformPackageRoots({
    servicePackages: registryManifest.services.map(service => service.pkg),
    hostPackage: '@kb-labs/host-app',
    platformMembers: config.platformMembers,
    adapterPackages: configuredAdapterPackages(config.adapters, packageManifests),
  });
  const shipped = shippedPlatformPackageSet(roots, packageManifests);

  assert.ok(shipped.has('@kb-labs/core-discovery'), 'core-runtime requires the discovery package at runtime');
  for (const packageName of shipped) {
    const manifest = packageManifests.get(packageName);
    for (const dependency of [
      ...Object.keys(manifest?.dependencies ?? {}),
      ...Object.keys(manifest?.optionalDependencies ?? {}),
    ]) {
      if (packageManifests.has(dependency)) {
        assert.ok(shipped.has(dependency), `${packageName} requires shipped dependency ${dependency}`);
      }
    }
  }
  const pluginPackages = new Set((registryManifest.plugins ?? []).map(plugin => plugin.pkg));
  assert.deepEqual([...shipped].filter(name => pluginPackages.has(name)), [],
    'platform dependency closure must not include registered plugin packages');
});
