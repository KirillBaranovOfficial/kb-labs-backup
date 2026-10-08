/**
 * @module @kb-labs/core-platform/adapters/adapter-manifest-loader
 * The single package adapter-manifest loader used by config, discovery and marketplace.
 */

import * as fs from 'node:fs/promises';
import * as path from 'node:path';
import { pathToFileURL } from 'node:url';
import type { AdapterManifest } from './adapter-manifest.js';

export type AdapterManifestLoadResult =
  | { kind: 'adapter'; manifest: AdapterManifest; modulePath: string }
  | { kind: 'not-adapter'; reason: string; modulePath?: string }
  | { kind: 'failed'; reason: string; modulePath?: string };

export type AdapterManifestSource =
  | { packageRoot: string }
  | { spec: string; platformRoot: string };

/** Read the sole supported package manifest declaration: `kb.manifest`. */
export function declaredManifestPath(pkgJson: Record<string, unknown>): string | undefined {
  const kb = pkgJson.kb;
  if (kb === null || typeof kb !== 'object' || Array.isArray(kb)) {return undefined;}
  const manifest = (kb as Record<string, unknown>).manifest;
  return typeof manifest === 'string' && manifest.trim() !== '' ? manifest : undefined;
}

function packageNameFromSpec(spec: string): string {
  const parts = spec.split('/');
  return parts.slice(0, spec.startsWith('@') ? 2 : 1).join('/');
}

export function resolveInstalledAdapterPackageRoot(spec: string, platformRoot: string): string {
  const name = packageNameFromSpec(spec);
  if (!name) {throw new Error(`Invalid adapter package specifier: ${spec}`);}
  return path.resolve(platformRoot, 'node_modules', name);
}

async function resolvePackageRoot(source: AdapterManifestSource): Promise<string> {
  if ('packageRoot' in source) {return path.resolve(source.packageRoot);}
  return resolveInstalledAdapterPackageRoot(source.spec, source.platformRoot);
}

/** Resolve the declared manifest module without loading it. */
export async function resolveAdapterManifestModulePath(source: AdapterManifestSource): Promise<string | undefined> {
  const packageRoot = await resolvePackageRoot(source);
  const pkg = JSON.parse(await fs.readFile(path.join(packageRoot, 'package.json'), 'utf8')) as Record<string, unknown>;
  const manifestField = declaredManifestPath(pkg);
  if (!manifestField) {return undefined;}
  const modulePath = path.resolve(packageRoot, manifestField);
  const relativePath = path.relative(packageRoot, modulePath);
  if (relativePath.startsWith('..') || path.isAbsolute(relativePath)) {
    throw new Error('declared manifest path escapes package root');
  }
  return modulePath;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function isAdapterManifest(value: unknown): value is AdapterManifest {
  if (!isRecord(value)) {return false;}
  return typeof value.manifestVersion === 'string' &&
    value.schema === 'kb.adapter/1' &&
    typeof value.id === 'string' &&
    typeof value.name === 'string' &&
    typeof value.version === 'string' &&
    typeof value.networkRequired === 'boolean' &&
    (value.type === 'core' || value.type === 'extension' || value.type === 'proxy') &&
    ((typeof value.implements === 'string' && value.implements.length > 0) ||
      (Array.isArray(value.implements) && value.implements.length > 0 && value.implements.every((role) => typeof role === 'string')));
}

/**
 * Resolve and load the declared adapter manifest for a workspace package or an
 * installed package under a platform root. Missing declarations and failed
 * imports remain distinct typed results for all callers.
 */
export async function loadAdapterManifest(source: AdapterManifestSource): Promise<AdapterManifestLoadResult> {
  let modulePath: string | undefined;
  try {
    modulePath = await resolveAdapterManifestModulePath(source);
    if (!modulePath) {
      return { kind: 'not-adapter', reason: 'package.json does not declare kb.manifest' };
    }
    const mod = await import(pathToFileURL(modulePath).href) as { manifest?: unknown };
    if (!isRecord(mod.manifest)) {
      return { kind: 'failed', reason: 'built module must export an object named manifest', modulePath };
    }
    const implementsAdapter =
      (typeof mod.manifest.implements === 'string' && mod.manifest.implements.length > 0) ||
      (Array.isArray(mod.manifest.implements) && mod.manifest.implements.length > 0 && mod.manifest.implements.every((role) => typeof role === 'string'));
    if (!implementsAdapter) {
      return { kind: 'not-adapter', reason: 'manifest does not declare implements', modulePath };
    }
    if (mod.manifest.schema !== 'kb.adapter/1') {
      const actualSchema = typeof mod.manifest.schema === 'string' ? `"${mod.manifest.schema}"` : '(missing)';
      return {
        kind: 'failed',
        reason: `adapter manifest must declare schema "kb.adapter/1"; received ${actualSchema}`,
        modulePath,
      };
    }
    if (typeof mod.manifest.networkRequired !== 'boolean') {
      return { kind: 'failed', reason: 'adapter manifest must declare networkRequired as a boolean', modulePath };
    }
    if (!isAdapterManifest(mod.manifest)) {
      return { kind: 'failed', reason: 'built module must export a valid AdapterManifest named manifest', modulePath };
    }
    return { kind: 'adapter', manifest: mod.manifest, modulePath };
  } catch (error) {
    return {
      kind: 'failed',
      reason: error instanceof Error ? error.message : String(error),
      ...(modulePath ? { modulePath } : {}),
    };
  }
}
