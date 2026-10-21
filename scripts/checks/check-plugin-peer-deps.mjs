#!/usr/bin/env node
/**
 * check-plugin-peer-deps — platform packages must be peerDependencies (not
 * dependencies) of plugin packages.
 *
 * Task 0.3. Rule id: `plugin-platform-dependency`.
 * Why: a plugin listing a platform package under `dependencies` makes the package
 * manager install its own private copy of the platform next to the host's, so the
 * plugin talks to a different instance than the one that loads it.
 * Reference: docs/architecture/target/03-domain-model.md (extension model).
 *
 * The sealed platform graph is derived from platform-release.jsonc and package
 * manifests by the same closure owner used by release-index generation. A plugin
 * may peer only on packages present in that graph; private plugin dependencies
 * stay ordinary dependencies. `devDependencies` are not inspected.
 *
 * Scope: plugin entry packages (ship a `kb.plugin/3` manifest; daemons/engines/core under
 * plugins/ are platform parts and out of scope) plus templates/plugin-template.
 * Pre-existing offenders live in scripts/checks/boundary-exceptions.json.
 *
 * Devkit custom check (runs once, anchored to @kb-labs/devkit).
 * Standalone: node scripts/checks/check-plugin-peer-deps.mjs [--root <dir>] [--json] [--no-exceptions]
 */

import { pathToFileURL } from 'node:url';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import {
  DEFAULT_HOST_PACKAGE,
  DEFAULT_PLATFORM_PACKAGE,
  DEFAULT_SDK_PACKAGE,
  configuredAdapterPackages,
  platformPackageRoots,
  platformSharedPackages,
  releaseArtifactPackageNames,
  readPlatformReleaseConfig,
  shippedPlatformPackageSet,
} from '../../tools/kb-create/scripts/platform-release-config.mjs';

import { findPackages, findPluginEntryPackages, runLint } from './lib/boundary-common.mjs';

export const CHECK_NAME = 'plugin-peer-deps';
export const RULE = 'plugin-platform-dependency';

function shippedPlatformPackages(root) {
  const packageList = findPackages(root, '.', 8);
  const extras = JSON.parse(readFileSync(join(root, 'e2e/scripts/pack-extras.json'), 'utf8'))
    .map(({ directory }) => ({ dir: join(root, directory), relDir: directory,
      json: JSON.parse(readFileSync(join(root, directory, 'package.json'), 'utf8')) }));
  const artifactSources = [...packageList, ...extras];
  const packageManifests = new Map(artifactSources.filter(pkg => pkg.json.name).map(pkg => [pkg.json.name, pkg.json]));
  const config = readPlatformReleaseConfig(join(root, 'tools/kb-create/config/platform-release.jsonc'));
  const registry = JSON.parse(readFileSync(join(root, 'e2e/platform/registry-manifest.json'), 'utf8'));
  const releaseNames = releaseArtifactPackageNames(registry, config.platformMembers);
  const releaseManifests = new Map(releaseNames.map(name => {
    const manifest = packageManifests.get(name);
    if (!manifest) throw new Error(`release artifact ${name} is absent from workspace package manifests`);
    return [name, manifest];
  }));
  const roots = platformPackageRoots({
    servicePackages: registry.services.map(service => service.pkg),
    platformMembers: config.platformMembers,
    adapterPackages: configuredAdapterPackages(config.adapters, packageManifests),
    platformPackage: DEFAULT_PLATFORM_PACKAGE,
    sdkPackage: DEFAULT_SDK_PACKAGE,
    sdkManifest: packageManifests.get(DEFAULT_SDK_PACKAGE),
    hostPackage: DEFAULT_HOST_PACKAGE,
  });
  const pluginPackages = new Set(registry.plugins.map(plugin => plugin.pkg));
  const shipped = shippedPlatformPackageSet(roots, releaseManifests, DEFAULT_PLATFORM_PACKAGE);
  return {
    packages: packageManifests,
    platformRoots: platformSharedPackages(shipped, pluginPackages, DEFAULT_PLATFORM_PACKAGE),
    shipped,
  };
}

/** @returns {{ target: string, field: string }[]} platform packages declared outside peerDependencies. */
export function findPlatformRuntimeDeps(pkgJson, platformRoots) {
  const out = [];
  for (const field of ['dependencies', 'optionalDependencies']) {
    for (const name of Object.keys(pkgJson[field] ?? {})) {
      if (platformRoots.has(name)) out.push({ target: name, field });
    }
  }
  return out;
}

export function collect(root) {
  const { packages: packageManifests, platformRoots, shipped } = shippedPlatformPackages(root);
  const pluginPackages = [
    ...findPluginEntryPackages(root),
    ...findPackages(root, 'templates/plugin-template', 2),
  ];
  const violations = [];
  for (const pkg of pluginPackages) {
    for (const { target, field } of findPlatformRuntimeDeps(pkg.json, platformRoots)) {
      violations.push({
        rule: RULE,
        package: pkg.relDir,
        target,
        file: `${pkg.relDir}/package.json`,
        message: `${pkg.relDir}: ${target} is in ${field} — platform packages must be peerDependencies in plugins`,
        fix: `Move ${target} to peerDependencies (keep a devDependencies entry for build/tests).`,
      });
    }
    for (const target of Object.keys(pkg.json.peerDependencies ?? {})) {
      if (packageManifests.has(target) && !shipped.has(target)) {
        violations.push({
          rule: RULE,
          package: pkg.relDir,
          target,
          file: `${pkg.relDir}/package.json`,
          message: `${pkg.relDir}: ${target} is a peer dependency but is not provided by the platform package graph`,
          fix: `Remove the unused peer or add ${target} to the release platform graph if the plugin needs it at runtime.`,
        });
      }
    }
  }
  return violations;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  process.exitCode = await runLint({ name: CHECK_NAME, rules: [RULE], collect });
}
