/**
 * @module @kb-labs/core-extension-store/pnpm-installer
 * The only place in this package that calls `pnpm` (mirrors
 * `plugins/marketplace/npm/src/npm-source.ts`).
 *
 * Installs one package into a synthetic single-dependency pnpm project, with
 * `autoInstallPeers: false` (ADR-0048 D2/D5: a store entry carries the package
 * and its own third-party dependencies, never a platform package — those are
 * wired at runtime by the resolve hook, not by installation layout) and a
 * dedicated content-addressed store directory (never the user's default pnpm
 * store, so a broken entry can never corrupt unrelated installs).
 */

import { execa } from 'execa';
import * as fs from 'node:fs/promises';
import * as path from 'node:path';
import * as crypto from 'node:crypto';
import { parse as parseYaml } from 'yaml';
import type { PackageInstallOptions, PackageInstallResult, PackageInstaller } from './types.js';
import { StoreInstallError } from './errors.js';

const INSTALL_TIMEOUT_MS = 5 * 60 * 1000;

export interface PnpmInstallerOptions {
  /** Overrides the `pnpm` executable, mainly for tests. Defaults to `'pnpm'`. */
  pnpmPath?: string;
  timeoutMs?: number;
}

/** Default `PackageInstaller`: a real `pnpm install` into a fresh synthetic project. */
export class PnpmInstaller implements PackageInstaller {
  private readonly pnpmPath: string;
  private readonly timeoutMs: number;

  constructor(options: PnpmInstallerOptions = {}) {
    this.pnpmPath = options.pnpmPath ?? 'pnpm';
    this.timeoutMs = options.timeoutMs ?? INSTALL_TIMEOUT_MS;
  }

  async install(options: PackageInstallOptions): Promise<PackageInstallResult> {
    const { name, version, projectDir, casDir } = options;
    const spec = options.spec ?? version;

    await fs.mkdir(projectDir, { recursive: true });
    await fs.mkdir(casDir, { recursive: true });
    await writeSyntheticProject(projectDir, name, spec);

    try {
      await execa(this.pnpmPath, ['install', '--store-dir', casDir], {
        cwd: projectDir,
        timeout: this.timeoutMs,
      });
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      throw new StoreInstallError({ plugin: `${name}@${version}` }, `pnpm install failed: ${message}`);
    }

    const packageRoot = await resolveInstalledPackageRoot(projectDir, name, version);
    const integrity =
      (await readIntegrityFromLockfile(projectDir, name, version)) ?? (await computeFallbackIntegrity(packageRoot));

    return { integrity, packageRoot };
  }
}

async function writeSyntheticProject(projectDir: string, name: string, spec: string): Promise<void> {
  const pkg = {
    name: 'kb-store-entry',
    private: true,
    version: '0.0.0',
    dependencies: { [name]: spec },
  };
  await fs.writeFile(path.join(projectDir, 'package.json'), `${JSON.stringify(pkg, null, 2)}\n`, 'utf-8');
  // autoInstallPeers: false — a store entry never resolves platform peers itself;
  // the host wires them at runtime with a resolve hook (ADR-0048 D5).
  await fs.writeFile(path.join(projectDir, 'pnpm-workspace.yaml'), 'autoInstallPeers: false\n', 'utf-8');
}

async function resolveInstalledPackageRoot(projectDir: string, name: string, version: string): Promise<string> {
  const installedPath = path.join(projectDir, 'node_modules', ...name.split('/'));
  try {
    return await fs.realpath(installedPath);
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    throw new StoreInstallError(
      { plugin: `${name}@${version}` },
      `pnpm install did not produce ${installedPath}: ${message}`,
    );
  }
}

/**
 * Reads the tarball integrity pnpm recorded for `name@version` in the synthetic
 * project's own `pnpm-lock.yaml`. Registry installs get a `sha512-...` value
 * this way; `file:`/`link:` specs (tests, local tarballs) typically have none,
 * in which case the caller falls back to hashing the installed package.
 */
async function readIntegrityFromLockfile(
  projectDir: string,
  name: string,
  version: string,
): Promise<string | undefined> {
  let raw: string;
  try {
    raw = await fs.readFile(path.join(projectDir, 'pnpm-lock.yaml'), 'utf-8');
  } catch {
    return undefined;
  }

  let parsed: unknown;
  try {
    parsed = parseYaml(raw);
  } catch {
    return undefined;
  }
  const packages = (parsed as { packages?: Record<string, unknown> } | undefined)?.packages;
  if (!packages || typeof packages !== 'object') {return undefined;}

  const prefix = `${name}@${version}`;
  for (const [key, value] of Object.entries(packages)) {
    // A key is the exact "name@version" or that prefix followed by a peer suffix
    // like "name@version(peer@1.0.0)" — never a different version's prefix.
    if (key !== prefix && !key.startsWith(`${prefix}(`)) {continue;}
    const integrity = (value as { resolution?: { integrity?: unknown } } | undefined)?.resolution?.integrity;
    if (typeof integrity === 'string' && integrity.length > 0) {
      return integrity;
    }
  }
  return undefined;
}

/** Fallback for specs with no registry integrity (`file:`/`link:`): hash the installed package.json. */
async function computeFallbackIntegrity(packageRoot: string): Promise<string> {
  const content = await fs.readFile(path.join(packageRoot, 'package.json'));
  const hash = crypto.createHash('sha256').update(content).digest('base64');
  return `sha256-${hash}`;
}
