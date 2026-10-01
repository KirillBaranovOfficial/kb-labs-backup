/**
 * @module @kb-labs/marketplace-npm/npm-source
 * pnpm-based PackageSource implementation.
 * This is the ONLY place in the marketplace codebase that calls pnpm.
 */

import { execa } from 'execa';
import * as path from 'node:path';
import * as fs from 'node:fs/promises';
import * as crypto from 'node:crypto';
import type {
  PackageSource,
  ResolvedPackage,
  InstalledPackage,
} from '@kb-labs/marketplace-contracts';

export class PackageMetadataError extends Error {
  readonly code: 'PACKAGE_NOT_FOUND' | 'VERSION_NOT_FOUND' | 'UNKNOWN_ENTITY_KIND' | 'METADATA_FETCH_FAILED';
  readonly packageId: string;
  readonly version: string;
  readonly hint: string;

  constructor(code: PackageMetadataError['code'], packageId: string, version: string, message: string) {
    super(message);
    this.name = 'PackageMetadataError';
    this.code = code;
    this.packageId = packageId;
    this.version = version;
    this.hint = `Publish ${packageId}@${version} with a kb.manifest or kb.adapter field, then run "kb marketplace install ${packageId}@${version}".`;
  }
}

export class PlatformArtifactPinsError extends Error {
  readonly code = 'MARKETPLACE_PLATFORM_ARTIFACT_PINS_MISSING';
  readonly hint: string;

  constructor(root: string, packageId: string, version: string) {
    super("The installed platform is missing kb-create's sealed artifact overrides. No package was installed.");
    this.name = 'PlatformArtifactPinsError';
    this.hint = `Run "kb-create update --platform-root ${JSON.stringify(root)}" and retry "kb marketplace install ${packageId}@${version} --scope platform".`;
  }
}

export interface NpmPackageSourceOptions {
  registryUrl?: string;
}

export class PackageInstallError extends Error {
  readonly code: 'NOT_FOUND' | 'INSTALL_FAILED';
  readonly spec: string;
  constructor(code: PackageInstallError['code'], spec: string, message: string) {
    super(message);
    this.name = 'PackageInstallError';
    this.code = code;
    this.spec = spec;
  }
}

export class NpmPackageSource implements PackageSource {
  private readonly registryUrl: string;

  constructor(options: NpmPackageSourceOptions = {}) {
    this.registryUrl = (options.registryUrl ?? process.env.npm_config_registry ?? process.env.NPM_CONFIG_REGISTRY ?? 'https://registry.npmjs.org').replace(/\/$/, '');
  }

  async resolve(spec: string): Promise<ResolvedPackage> {
    const id = extractPackageName(spec);
    if (!id) {
      throw new Error(`Invalid package spec: ${spec}`);
    }

    const requestedVersion = extractVersion(spec);
    const packageURL = `${this.registryUrl}/${encodeURIComponent(id).replace('%40', '@')}`;
    let response: Response;
    try {
      response = await globalThis.fetch(packageURL);
    } catch (error) {
      throw new PackageMetadataError('METADATA_FETCH_FAILED', id, requestedVersion ?? 'latest',
        `Could not read package metadata for ${id}: ${error instanceof Error ? error.message : String(error)}`);
    }
    if (response.status === 404) {
      throw new PackageMetadataError('PACKAGE_NOT_FOUND', id, requestedVersion ?? 'latest', `Package ${id} was not found in ${this.registryUrl}`);
    }
    if (!response.ok) {
      throw new PackageMetadataError('METADATA_FETCH_FAILED', id, requestedVersion ?? 'latest', `Registry returned HTTP ${response.status} while reading ${id}`);
    }
    const packument = await response.json() as {
      'dist-tags'?: { latest?: string };
      versions?: Record<string, { kb?: { manifest?: string; adapter?: unknown } }>;
    };
    const version = requestedVersion ?? packument['dist-tags']?.latest;
    const manifest = version ? packument.versions?.[version]?.kb : undefined;
    if (!version || !manifest) {
      throw new PackageMetadataError('VERSION_NOT_FOUND', id, requestedVersion ?? 'latest',
        `Package ${id} has no published metadata for ${requestedVersion ?? 'latest'}`);
    }
    const primaryKind = manifest.adapter ? 'adapter' : manifest.manifest ? 'plugin' : undefined;
    if (!primaryKind) {
      throw new PackageMetadataError('UNKNOWN_ENTITY_KIND', id, version,
        `Package ${id}@${version} does not declare kb.manifest or kb.adapter metadata`);
    }

    return {
      id,
      version,
      integrity: '', // computed after install
      source: 'marketplace',
      primaryKind,
    };
  }

  async install(
    pkg: ResolvedPackage,
    root: string,
    opts?: { dev?: boolean },
  ): Promise<InstalledPackage> {
    await assertSealedPlatformOverrides(root, pkg.id, pkg.version);
    const spec = pkg.version === 'latest' ? pkg.id : `${pkg.id}@${pkg.version}`;
    const args = ['add', spec];
    if (opts?.dev) {args.push('--save-dev');}
    // Bypass the pnpm workspace-root guard: kb-create projects are flat (single-package),
    // so adding to the project root is intentional. Without this flag pnpm throws
    // ERR_PNPM_ADDING_TO_ROOT when a pnpm-workspace.yaml exists in the project directory.
    args.push('--ignore-workspace-root-check');

    try {
      await execa('pnpm', args, { cwd: root, timeout: 5 * 60 * 1000 });
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      const code = msg.includes('ERR_PNPM_FETCH_404') || msg.includes('Not Found - 404')
        ? 'NOT_FOUND'
        : 'INSTALL_FAILED';
      throw new PackageInstallError(code, spec, `pnpm add ${spec} failed: ${msg}`);
    }

    const packageRoot = path.join(root, 'node_modules', pkg.id);

    // Read actual installed version
    let version = pkg.version;
    try {
      const pkgJson = JSON.parse(await fs.readFile(path.join(packageRoot, 'package.json'), 'utf-8'));
      version = pkgJson.version ?? version;
    } catch { /* use spec version */ }

    // Compute integrity
    const integrity = await computeIntegrity(packageRoot);

    return {
      id: pkg.id,
      version,
      packageRoot,
      integrity,
    };
  }

  async remove(packageId: string, root: string): Promise<void> {
    await execa('pnpm', ['remove', packageId], { cwd: root, timeout: 2 * 60 * 1000 });
  }
}

/** Check that kb-create owns and has written every sealed archive override. */
export async function assertSealedPlatformOverrides(root: string, packageId: string, version: string): Promise<void> {
  const receiptPath = path.join(root, '.kb', 'v2', 'receipt.json');
  const workspacePath = path.join(root, 'pnpm-workspace.yaml');
  try {
    await fs.access(receiptPath);
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === 'ENOENT') { return; }
    throw error;
  }
  let receipt: { plan?: { artifacts?: Array<{ package?: unknown; sha256?: unknown; tarball?: unknown; kind?: unknown }> } };
  receipt = JSON.parse(await fs.readFile(receiptPath, 'utf8')) as typeof receipt;
  const sealed = new Map<string, string>();
  for (const artifact of receipt.plan?.artifacts ?? []) {
    if (artifact.kind === 'binary' || typeof artifact.tarball !== 'string' || !artifact.tarball) { continue; }
    if (typeof artifact.package !== 'string' || typeof artifact.sha256 !== 'string' || !artifact.sha256) {
      throw new Error('Installed platform receipt contains an invalid sealed artifact identity. Run "kb-create doctor --platform-root <platform-root>".');
    }
    sealed.set(artifact.package, `file:.kb/v2/cache/packages/${artifact.sha256}.tgz`);
  }
  if (sealed.size === 0) { throw new PlatformArtifactPinsError(root, packageId, version); }
  let workspace;
  try {
    workspace = await fs.readFile(workspacePath, 'utf8');
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === 'ENOENT') {
      throw new PlatformArtifactPinsError(root, packageId, version);
    }
    throw error;
  }
  for (const [name, spec] of sealed) {
    const key = `(?:${escapeRegExp(JSON.stringify(name))}|'${escapeRegExp(name)}'|${escapeRegExp(name)})`;
    const value = `(?:${escapeRegExp(JSON.stringify(spec))}|'${escapeRegExp(spec)}'|${escapeRegExp(spec)})`;
    if (!new RegExp(`^  ${key}:\\s*${value}\\s*(?:#.*)?$`, 'm').test(workspace)) {
      throw new PlatformArtifactPinsError(root, packageId, version);
    }
  }
}

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function extractPackageName(spec: string): string | null {
  if (!spec || spec.startsWith('.') || spec.startsWith('/')) {return null;}

  if (spec.startsWith('@')) {
    const slashIndex = spec.indexOf('/');
    if (slashIndex === -1) {return null;}
    const versionSep = spec.lastIndexOf('@');
    return versionSep > slashIndex ? spec.slice(0, versionSep) : spec;
  }

  const versionSep = spec.indexOf('@');
  return versionSep > 0 ? spec.slice(0, versionSep) : spec;
}

function extractVersion(spec: string): string | null {
  if (spec.startsWith('@')) {
    const slashIndex = spec.indexOf('/');
    const lastAt = spec.lastIndexOf('@');
    return lastAt > slashIndex ? spec.slice(lastAt + 1) : null;
  }
  const atIndex = spec.indexOf('@');
  return atIndex > 0 ? spec.slice(atIndex + 1) : null;
}

async function computeIntegrity(packageRoot: string): Promise<string> {
  try {
    const content = await fs.readFile(path.join(packageRoot, 'package.json'));
    const hash = crypto.createHash('sha256').update(content).digest('base64');
    return `sha256-${hash}`;
  } catch {
    return '';
  }
}
