/**
 * @module @kb-labs/core-runtime/discover-adapters
 * Lock-based adapter discovery — reads from .kb/marketplace.lock.
 *
 * All adapters must be registered via `kb marketplace link` or `kb marketplace install`.
 * No filesystem scanning.
 *
 * Adapters are platform-owned. Project locks are never read for adapter
 * discovery; project locks contain plugins only.
 */

import { promises as fs } from 'node:fs';
import { pathToFileURL } from 'node:url';
import path from 'node:path';
import {
  readMarketplaceLock,
  DiagnosticCollector,
  findWorkspacePackages,
  resolveMarketplaceLockScope,
} from '@kb-labs/core-discovery';
import { loadAdapterManifest } from '@kb-labs/core-platform';
import type { AdapterManifest } from '@kb-labs/core-platform';
import { configuredWorkspaceAdapterInvalidError, configuredWorkspaceAdapterNotBuiltError } from './adapter-module-loader.js';

/**
 * Discovered adapter info
 */
export interface DiscoveredAdapter {
  /** Package name (e.g., "@kb-labs/adapters-openai") */
  packageName: string;
  /** Absolute path to package root */
  pkgRoot: string;
  /** Adapter factory function */
  createAdapter: (config?: unknown) => unknown;
  /** Adapter module (full exports) */
  module: Record<string, unknown> & { manifest: AdapterManifest };
}

/**
 * Load adapter module by file path (ESM import)
 */
async function loadAdapterModule(distPath: string): Promise<Record<string, unknown>> {
  const fileUrl = pathToFileURL(distPath).href;
  return import(fileUrl);
}

/**
 * Load adapters from a single marketplace.lock file into the discovered map.
 *
 * @param root - Directory containing .kb/marketplace.lock
 * @param discovered - Map to populate
 */
async function loadAdaptersFromLock(
  root: string,
  discovered: Map<string, DiscoveredAdapter>,
): Promise<void> {
  const diag = new DiagnosticCollector();
  const scope = await resolveMarketplaceLockScope({ platformRoot: root }, root);
  const lock = await readMarketplaceLock(root, diag, scope);
  if (!lock) { return; }

  for (const [pkgId, entry] of Object.entries(lock.adapters ?? {})) {
    if (entry.enabled === false) { continue; }

    if (!entry.resolvedPath) {continue;}
    const pkgRoot = path.resolve(root, entry.resolvedPath);

    // Read main export path and npm package name from package.json.
    // The npm package name may differ from pkgId (manifest ID) — e.g. lock key
    // is "kblabs-gateway-llm" but npm package is "@kb-labs/adapters-kblabs-gateway".
    let mainPath = 'dist/index.js';
    let npmPkgName: string | undefined;
    try {
      const pkgContent = await fs.readFile(path.join(pkgRoot, 'package.json'), 'utf-8');
      const pkg = JSON.parse(pkgContent) as { main?: string; name?: string };
      mainPath = pkg.main || mainPath;
      npmPkgName = pkg.name;
    } catch { /* use defaults */ }

    const distPath = path.join(pkgRoot, mainPath);

    try {
      await fs.access(distPath);
      const module = await loadAdapterModule(distPath);

      const manifestResult = await loadAdapterManifest({ packageRoot: pkgRoot });
      if (manifestResult.kind !== 'adapter') {continue;}

      if (typeof module.createAdapter !== 'function') {
        continue;
      }

      const adapterEntry: DiscoveredAdapter = {
        packageName: npmPkgName ?? pkgId,
        pkgRoot,
        createAdapter: module.createAdapter as (config?: unknown) => unknown,
        module: { ...module, manifest: manifestResult.manifest },
      };

      // Index by manifest ID and npm package name.
      discovered.set(pkgId, adapterEntry);
      if (npmPkgName && npmPkgName !== pkgId) {
        discovered.set(npmPkgName, adapterEntry);
      }
    } catch {
      // Skip adapters that fail to load (not built yet)
    }
  }
}

/** Load built adapter packages from the pnpm workspace development source. */
async function loadWorkspaceAdapters(
  root: string,
  discovered: Map<string, DiscoveredAdapter>,
  configuredPackages: readonly { packageName: string; slot: string }[],
): Promise<void> {
  const packages = await findWorkspacePackages(root);
  const requested = new Map(configuredPackages.map(adapter => [adapter.packageName, adapter.slot]));
  for (const [packageName, slot] of requested) {
    const candidate = packages.find(pkg => pkg.packageName === packageName);
    if (!candidate) {continue;}
    const distPath = path.join(candidate.packageRoot, 'dist', 'index.js');
    try {
      await fs.access(distPath);
    } catch {
      throw configuredWorkspaceAdapterNotBuiltError(slot, packageName);
    }
    const manifestResult = await loadAdapterManifest({ packageRoot: candidate.packageRoot });
    if (manifestResult.kind !== 'adapter') {
      throw configuredWorkspaceAdapterInvalidError(slot, packageName, manifestResult.reason);
    }
    const module = await loadAdapterModule(distPath);
    if (typeof module.createAdapter !== 'function') {
      throw configuredWorkspaceAdapterInvalidError(slot, packageName, 'its module does not export createAdapter');
    }
    discovered.set(packageName, {
      packageName,
      pkgRoot: candidate.packageRoot,
      createAdapter: module.createAdapter as (config?: unknown) => unknown,
      module: { ...module, manifest: manifestResult.manifest },
    });
  }
}

export interface WorkspaceAdapterDiscoveryOptions {
  /** True only when the platform config identifies this checkout as development mode. */
  development: boolean;
  /** Adapter package paths configured by the platform, including subpath entries. */
  configuredPackages: readonly { packageName: string; slot: string }[];
}

/** Load a channel factory from the same package source used for platform adapters. */
export async function loadAdapterChannelFactory(
  channelType: string,
  platformRoot: string,
  development: boolean,
): Promise<(config: unknown, channelId: string) => unknown> {
  const candidateRoots: string[] = [];
  if (development) {
    const packages = await findWorkspacePackages(platformRoot);
    candidateRoots.push(...packages.map(pkg => pkg.packageRoot));
  } else {
    const diagnostic = new DiagnosticCollector();
    const scope = await resolveMarketplaceLockScope({ platformRoot }, platformRoot);
    const lock = await readMarketplaceLock(platformRoot, diagnostic, scope);
    for (const entry of Object.values(lock?.adapters ?? {})) {
      if (entry.enabled && entry.resolvedPath) {
        candidateRoots.push(path.resolve(platformRoot, entry.resolvedPath));
      }
    }
  }

  const matches: string[] = [];
  for (const candidateRoot of candidateRoots) {
    const manifestResult = await loadAdapterManifest({ packageRoot: candidateRoot });
    if (manifestResult.kind !== 'adapter') { continue; }
    const implementsValue = manifestResult.manifest.implements;
    const interfaces = Array.isArray(implementsValue) ? implementsValue : [implementsValue];
    if (manifestResult.manifest.channelType === channelType && interfaces.includes('INotifierChannel')) {
      matches.push(candidateRoot);
    }
  }
  if (matches.length !== 1) {
    const reason = matches.length === 0 ? 'no installed adapter manifest matches this channel type' : 'multiple adapter manifests match this channel type';
    throw new Error(`Notifier channel type "${channelType}" is unavailable: ${reason}.`);
  }
  const packageRoot = matches[0]!;

  const packageMetadata = JSON.parse(
    await fs.readFile(path.join(packageRoot, 'package.json'), 'utf-8'),
  ) as { main?: string };
  const modulePath = path.resolve(packageRoot, packageMetadata.main ?? 'dist/index.js');
  const relativePath = path.relative(path.resolve(packageRoot), modulePath);
  if (relativePath.startsWith('..') || path.isAbsolute(relativePath)) {
    throw new Error(`Notifier channel type "${channelType}" has a main path outside its package root.`);
  }
  const module = await loadAdapterModule(modulePath) as {
    createChannel?: unknown;
    default?: unknown;
  };
  const factory = module.createChannel ?? module.default;
  if (typeof factory !== 'function') {
    throw new Error(`Notifier channel type "${channelType}" does not export createChannel.`);
  }
  return factory as (config: unknown, channelId: string) => unknown;
}

/**
 * Discover adapters from marketplace.lock(s).
 * Reads entries with `primaryKind === 'adapter'` and loads their modules.
 *
 * @param platformRoot - Platform installation / workspace root directory
 * @returns Map of package names to adapter info
 */
export async function discoverAdapters(
  platformRoot: string,
  workspaceOptions?: WorkspaceAdapterDiscoveryOptions,
): Promise<Map<string, DiscoveredAdapter>> {
  const discovered = new Map<string, DiscoveredAdapter>();
  await loadAdaptersFromLock(platformRoot, discovered);
  if (workspaceOptions?.development) {
    await loadWorkspaceAdapters(platformRoot, discovered, workspaceOptions.configuredPackages);
  }

  return discovered;
}

/**
 * Resolve adapter path — reads from marketplace.lock, supports subpath exports.
 *
 * @param adapterPath - Package name or subpath (e.g., "@kb-labs/adapters-openai/embeddings")
 * @param platformRoot - Platform installation root
 * @returns Adapter factory function
 */
export async function resolveAdapter(
  adapterPath: string,
  platformRoot: string,
): Promise<((config?: unknown) => unknown) | null> {
  const discovered = await discoverAdapters(platformRoot);

  // Check for subpath exports (e.g., "@kb-labs/adapters-openai/embeddings")
  const basePkgName = adapterPath.split('/').slice(0, 2).join('/');
  const subpath = adapterPath.includes('/')
    ? adapterPath.split('/').slice(2).join('/')
    : null;

  const adapter = discovered.get(basePkgName);

  if (adapter && subpath) {
    const subpathFile = path.join(adapter.pkgRoot, 'dist', `${subpath}.js`);
    try {
      await fs.access(subpathFile);
      const module = await loadAdapterModule(subpathFile);
      if (typeof module.createAdapter === 'function') {return module.createAdapter as (config?: unknown) => unknown;}
      if (typeof module.default === 'function') {return module.default as (config?: unknown) => unknown;}
    } catch { /* subpath not found */ }
  } else if (adapter) {
    return adapter.createAdapter;
  }

  // No fallback — all adapters must be registered in marketplace.lock
  return null;
}
