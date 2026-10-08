/**
 * @module @kb-labs/core-discovery/workspace-source
 * Development source: pnpm workspace packages that declare a plugin manifest.
 *
 * This is not a second discovery pipeline. It only enumerates candidate package
 * roots (the monorepo checkout the developer works in); every candidate goes
 * through the same manifest loading, validation and shadowing as a lock entry.
 */

import * as fs from 'node:fs/promises';
import * as path from 'node:path';
import { glob } from 'glob';
import { parse as parseYaml } from 'yaml';
import { pathToFileURL } from 'node:url';
import { declaredManifestPath } from '@kb-labs/plugin-contracts';
import { DISCOVERY_PACKAGE_MANIFEST, DISCOVERY_WORKSPACE_FILE } from './discovery-inputs.js';

export interface WorkspaceCandidate {
  /** `name` from package.json */
  packageName: string;
  /** Absolute package directory */
  packageRoot: string;
}

export type AdapterManifestLoadResult =
  | { kind: 'adapter'; manifest: Record<string, unknown>; modulePath: string }
  | { kind: 'not-adapter'; reason: string; modulePath?: string }
  | { kind: 'failed'; reason: string; modulePath?: string };

/** Resolve the package's built manifest module using discovery's package metadata rules. */
export async function resolveManifestModulePath(packageRoot: string): Promise<string | undefined> {
  const rawPackage = JSON.parse(await fs.readFile(path.join(packageRoot, DISCOVERY_PACKAGE_MANIFEST), 'utf-8')) as Record<string, unknown>;
  const manifestField = declaredManifestPath(rawPackage);
  if (!manifestField) {return undefined;}
  const distPath = path.resolve(packageRoot, manifestField);
  const relativePath = path.relative(path.resolve(packageRoot), distPath);
  if (relativePath.startsWith('..') || path.isAbsolute(relativePath)) {
    throw new Error('declared manifest path escapes package root');
  }
  return distPath;
}

/** Load the adapter manifest through the same built module discovery will use. */
export async function loadAdapterManifest(packageRoot: string): Promise<AdapterManifestLoadResult> {
  let modulePath: string | undefined;
  try {
    modulePath = await resolveManifestModulePath(packageRoot);
    if (!modulePath) {
      return { kind: 'not-adapter', reason: 'package.json does not declare kb.manifest' };
    }
    await fs.access(modulePath);
    const mod = await import(pathToFileURL(modulePath).href) as { manifest?: unknown };
    if (!mod.manifest || typeof mod.manifest !== 'object' || Array.isArray(mod.manifest)) {
      return { kind: 'failed', reason: 'built module must export an object named manifest', modulePath };
    }
    const manifest = mod.manifest as Record<string, unknown>;
    const implementsAdapter =
      (typeof manifest.implements === 'string' && manifest.implements.length > 0) ||
      (Array.isArray(manifest.implements) && manifest.implements.length > 0);
    if (!implementsAdapter) {
      return { kind: 'not-adapter', reason: 'manifest does not declare implements', modulePath };
    }
    return { kind: 'adapter', manifest, modulePath };
  } catch (error) {
    return {
      kind: 'failed',
      reason: error instanceof Error ? error.message : String(error),
      ...(modulePath ? { modulePath } : {}),
    };
  }
}

const IGNORED_DIRS = ['.kb/**', '**/node_modules/**'];

async function readPackageJson(file: string): Promise<Record<string, unknown> | null> {
  try {
    return JSON.parse(await fs.readFile(file, 'utf-8')) as Record<string, unknown>;
  } catch {
    return null;
  }
}

async function exists(file: string): Promise<boolean> {
  try {
    await fs.access(file);
    return true;
  } catch {
    return false;
  }
}

/**
 * A package is a candidate when it declares a manifest and that manifest (or its
 * static JSON sibling) has been built. Unbuilt packages are skipped silently:
 * a fresh checkout has hundreds of them and none is an error.
 */
async function toCandidate(packageRoot: string): Promise<WorkspaceCandidate | null> {
  const pkg = await readPackageJson(path.join(packageRoot, DISCOVERY_PACKAGE_MANIFEST));
  if (!pkg || typeof pkg.name !== 'string') {return null;}
  let manifestFile: string | undefined;
  try {
    manifestFile = await resolveManifestModulePath(packageRoot);
  } catch {
    return null;
  }
  if (!manifestFile) {return null;}
  const built = await exists(manifestFile) || await exists(manifestFile.replace(/\.[cm]?js$/, '.json'));
  return built ? { packageName: pkg.name, packageRoot } : null;
}

async function workspacePackageRoots(
  root: string,
  onInputRead?: (filePath: string) => void,
): Promise<string[]> {
  const absRoot = path.resolve(root);

  let patterns: string[] | null = null;
  try {
    const workspaceFile = path.join(absRoot, DISCOVERY_WORKSPACE_FILE);
    onInputRead?.(workspaceFile);
    const parsed = parseYaml(await fs.readFile(workspaceFile, 'utf-8')) as
      | { packages?: unknown }
      | null;
    if (Array.isArray(parsed?.packages)) {
      patterns = parsed.packages.filter((p): p is string => typeof p === 'string');
    }
  } catch {
    // no workspace file — handled below
  }

  if (!patterns) {
    return [absRoot];
  }

  const include = patterns.filter(p => !p.startsWith('!'));
  const exclude = patterns.filter(p => p.startsWith('!')).map(p => `${p.slice(1).replace(/\/$/, '')}/**`);
  const roots = new Set<string>();
  for (const pattern of include) {
    const files = await glob(path.posix.join(pattern.replace(/\/$/, ''), DISCOVERY_PACKAGE_MANIFEST), {
      cwd: absRoot,
      ignore: [...IGNORED_DIRS, ...exclude],
    });
    for (const file of files) {
      roots.add(path.dirname(path.join(absRoot, file)));
    }
  }
  return [...roots].sort();
}

/** Enumerate every package matched by a workspace, whether built or not. */
export async function findWorkspacePackages(root: string): Promise<WorkspaceCandidate[]> {
  const packages = await Promise.all((await workspacePackageRoots(root)).map(async packageRoot => {
    const pkg = await readPackageJson(path.join(packageRoot, DISCOVERY_PACKAGE_MANIFEST));
    return pkg && typeof pkg.name === 'string' ? { packageName: pkg.name, packageRoot } : null;
  }));
  return packages.filter((candidate): candidate is WorkspaceCandidate => candidate !== null);
}

/**
 * Enumerate the workspace packages under `root` that declare a plugin manifest.
 *
 * - `pnpm-workspace.yaml` present: every package matched by its `packages` globs
 *   (negated `!` patterns are honoured).
 * - absent: the root package itself, so a single plugin repository is also a
 *   development source.
 *
 * Results are sorted by package root for a deterministic order.
 */
export async function findWorkspaceCandidates(
  root: string,
  onInputRead?: (filePath: string) => void,
): Promise<WorkspaceCandidate[]> {
  const candidates = await Promise.all((await workspacePackageRoots(root, onInputRead)).map(async (packageRoot) => {
    const packagePath = path.join(packageRoot, DISCOVERY_PACKAGE_MANIFEST);
    onInputRead?.(packagePath);
    return toCandidate(packageRoot);
  }));
  return candidates.filter((c): c is WorkspaceCandidate => c !== null);
}
