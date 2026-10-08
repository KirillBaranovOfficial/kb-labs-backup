/**
 * Tests for MarketplaceService — the unified install/uninstall/link/sync/doctor API.
 *
 * Strategy: mock PackageSource + real filesystem (tmpDir).
 * We don't mock core-discovery — we use real lock files and kb.plugin.json.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import * as fs from 'node:fs/promises';
import * as path from 'node:path';
import * as os from 'node:os';
import * as crypto from 'node:crypto';
import type { PackageSource, InstalledPackage, ResolvedPackage, MarketplaceEntryWithId } from '@kb-labs/marketplace-contracts';
import type { IExtensionStore, ResolvedStoreEntry } from '@kb-labs/core-extension-store';
import { readMarketplaceLock, DiagnosticCollector } from '@kb-labs/core-discovery';
import { NoOpLogger } from '@kb-labs/core-platform/noop';
import { MarketplaceService, mergeScopedEntries, type MarketplaceServiceOptions } from '../marketplace-service.js';

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function makeMarketplaceService(options: Omit<MarketplaceServiceOptions, 'logger'>): MarketplaceService {
  return new MarketplaceService({ ...options, logger: new NoOpLogger() });
}

function computeIntegrity(content: string): string {
  return `sha256-${crypto.createHash('sha256').update(Buffer.from(content)).digest('base64')}`;
}

async function createPluginDir(
  tmpDir: string,
  name: string,
  id: string,
  version = '1.0.0',
): Promise<{ dir: string; integrity: string }> {
  const dir = path.join(tmpDir, 'packages', name);
  await fs.mkdir(path.join(dir, 'dist'), { recursive: true });
  await fs.writeFile(path.join(dir, 'dist', 'h.js'), 'export default async () => 0;');

  const pkgJson = JSON.stringify({ name: id, version });
  await fs.writeFile(path.join(dir, 'package.json'), pkgJson);
  await fs.writeFile(
    path.join(dir, 'kb.plugin.json'),
    JSON.stringify({
      schema: 'kb.plugin/3',
      id,
      version,
      cli: { commands: [{ id: 'hello', path: 'hello', describe: 'hi', handler: './h.js' }] },
    }),
  );

  return { dir, integrity: computeIntegrity(pkgJson) };
}

async function createAdapterDir(tmpDir: string, name: string, id: string): Promise<string> {
  const dir = path.join(tmpDir, 'packages', name);
  await fs.mkdir(path.join(dir, 'dist'), { recursive: true });
  await fs.writeFile(path.join(dir, 'package.json'), JSON.stringify({
    name: id,
    version: '1.0.0',
    kb: { manifest: './dist/adapter-manifest.js' },
  }));
  await fs.writeFile(path.join(dir, 'dist', 'adapter-manifest.js'), `export const manifest = ${JSON.stringify({
    schema: 'kb.adapter/1', manifestVersion: '1.0.0', id, name: id, version: '1.0.0', type: 'core', implements: 'ITestAdapter', networkRequired: false,
  })};`);
  return dir;
}

/**
 * Create a package directory that is NOT a KB Labs entity — just a plain
 * npm package with package.json and no kb.plugin.json / manifest.
 * Mirrors what happens when `install mind` fetches the unrelated npm `mind`
 * library instead of @kb-labs/mind-entry (B-021).
 */
async function createNonKbPackageDir(
  tmpDir: string,
  name: string,
  id: string,
  version = '0.0.2',
): Promise<{ dir: string; integrity: string }> {
  const dir = path.join(tmpDir, 'packages', name);
  await fs.mkdir(dir, { recursive: true });
  const pkgJson = JSON.stringify({ name: id, version });
  await fs.writeFile(path.join(dir, 'package.json'), pkgJson);
  // Intentionally NO kb.plugin.json / manifest.
  return { dir, integrity: computeIntegrity(pkgJson) };
}

function createMockSource(pluginDirs: Map<string, { dir: string; version: string; integrity: string }>): PackageSource {
  return {
    async resolve(spec: string): Promise<ResolvedPackage> {
      const info = pluginDirs.get(spec);
      if (!info) { throw new Error(`Package not found: ${spec}`); }
      return {
        id: spec,
        version: info.version,
        integrity: info.integrity,
        source: 'marketplace',
        primaryKind: 'plugin',
      };
    },
    async install(pkg: ResolvedPackage, _root: string): Promise<InstalledPackage> {
      const info = pluginDirs.get(pkg.id);
      if (!info) { throw new Error(`Package not found: ${pkg.id}`); }
      return {
        id: pkg.id,
        version: pkg.version,
        packageRoot: info.dir,
        integrity: pkg.integrity,
      };
    },
    async remove(_packageId: string, _root: string): Promise<void> {
      // noop in tests
    },
  };
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe('MarketplaceService', () => {
  let tmpDir: string;

  beforeEach(async () => {
    tmpDir = await fs.mkdtemp(path.join(os.tmpdir(), 'kb-marketplace-svc-'));
  });

  afterEach(async () => {
    await fs.rm(tmpDir, { recursive: true, force: true });
  });

  // ── install ──────────────────────────────────────────────────────────

  describe('install', () => {
    it('rejects packages without source metadata before installing', async () => {
      const install = vi.fn(async (): Promise<InstalledPackage> => {
        throw new Error('must not install an unclassified package');
      });
      const source: PackageSource = {
        async resolve(): Promise<ResolvedPackage> {
          return { id: '@kb-labs/unknown-entry', version: '1.0.0', integrity: '', source: 'marketplace' };
        },
        install,
        async remove() {},
      };
      const service = makeMarketplaceService({ platformRoot: tmpDir, source });
      const { PackageClassificationError } = await import('../marketplace-service.js');

      await expect(service.install({ scope: 'platform' }, ['@kb-labs/unknown-entry']))
        .rejects.toBeInstanceOf(PackageClassificationError);
      expect(install).not.toHaveBeenCalled();
    });

    it('installs project pins into the shared store and changes only marketplace.lock', async () => {
      const projectRoot = path.join(tmpDir, 'project');
      await fs.mkdir(path.join(projectRoot, '.kb'), { recursive: true });
      await fs.writeFile(path.join(projectRoot, '.kb', 'kb.config.json'), '{}');
      const { dir, integrity } = await createPluginDir(tmpDir, 'store-plugin', '@kb-labs/store-plugin');
      const source = createMockSource(new Map([
        ['@kb-labs/store-plugin', { dir, version: '1.0.0', integrity }],
      ]));
      const resolved = await source.resolve('@kb-labs/store-plugin');
      const storeEntry = {
        schema: 'kb.store-entry/1' as const,
        name: resolved.id,
        version: resolved.version,
        kind: 'plugin' as const,
        integrity,
        installedAt: new Date().toISOString(),
      };
      const stored: ResolvedStoreEntry = { entry: storeEntry, packageRoot: dir, entryDir: tmpDir };
      const store: IExtensionStore = {
        root: path.join(tmpDir, 'store'),
        casDir: path.join(tmpDir, 'store', 'cas'),
        entriesDir: path.join(tmpDir, 'store', 'entries'),
        async lookup() { return undefined; },
        async resolve() { return stored; },
        async listEntries() { return [storeEntry]; },
      };
      const sourceInstall = vi.spyOn(source, 'install');
      const sourceRemove = vi.spyOn(source, 'remove');
      const service = makeMarketplaceService({ platformRoot: tmpDir, source, extensionStore: store });

      await service.install({ scope: 'project', projectRoot }, ['@kb-labs/store-plugin']);
      await service.uninstall({ scope: 'project', projectRoot }, ['@kb-labs/store-plugin']);

      expect(sourceInstall).not.toHaveBeenCalled();
      expect(sourceRemove).not.toHaveBeenCalled();
      expect(await fs.readdir(projectRoot)).toEqual(['.kb']);
      expect(await fs.readdir(path.join(projectRoot, '.kb'))).toEqual(['kb.config.json', 'marketplace.lock']);
    });

    it('installs a plugin and writes to marketplace.lock', async () => {
      const { dir, integrity } = await createPluginDir(tmpDir, 'test-plugin', '@kb-labs/test-plugin');
      const source = createMockSource(
        new Map([['@kb-labs/test-plugin', { dir, version: '1.0.0', integrity }]]),
      );

      const service = makeMarketplaceService({ platformRoot: tmpDir, source });
      const result = await service.install({ scope: "platform" }, ['@kb-labs/test-plugin']);

      expect(result.installed).toHaveLength(1);
      expect(result.installed[0]!.id).toBe('@kb-labs/test-plugin');
      expect(result.installed[0]!.version).toBe('1.0.0');
      expect(result.installed[0]!.primaryKind).toBe('plugin');
      expect(result.installed[0]!.provides).toContain('plugin');
      expect(result.installed[0]!.provides).toContain('cli-command');
      expect(result.warnings).toHaveLength(0);

      // Verify lock file
      const lock = await readMarketplaceLock(tmpDir, new DiagnosticCollector());
      expect(lock).not.toBeNull();
      expect(lock!.plugins['@kb-labs/test-plugin']).toBeDefined();
      expect(lock!.plugins['@kb-labs/test-plugin']!.version).toBe('1.0.0');
      expect(lock!.plugins['@kb-labs/test-plugin']!.source).toBe('platform');
    });

    it('installs platform-scope plugin entry packages in the shared store', async () => {
      const packageId = '@kb-labs/custom-plugin';
      const { dir, integrity } = await createPluginDir(tmpDir, 'platform-plugin', packageId);
      const source: PackageSource = {
        async resolve(): Promise<ResolvedPackage> {
          return { id: packageId, version: '1.0.0', integrity, source: 'marketplace', primaryKind: 'plugin' };
        },
        async install(): Promise<InstalledPackage> { throw new Error('platform plugin must use the extension store'); },
        async remove() {},
      };
      const sourceInstall = vi.spyOn(source, 'install');
      const storeResolve = vi.fn(async () => {
        return {
          entry: {
            schema: 'kb.store-entry/1' as const,
            name: packageId,
            version: '1.0.0',
            kind: 'plugin' as const,
            integrity,
            installedAt: new Date().toISOString(),
          },
          packageRoot: dir,
          entryDir: path.join(tmpDir, 'store', 'entries', 'platform-plugin'),
        };
      });
      const store: IExtensionStore = {
        root: path.join(tmpDir, 'store'),
        casDir: path.join(tmpDir, 'store', 'cas'),
        entriesDir: path.join(tmpDir, 'store', 'entries'),
        async lookup() { return undefined; },
        resolve: storeResolve,
        async listEntries() { return []; },
      };
      const service = makeMarketplaceService({ platformRoot: tmpDir, source, extensionStore: store });

      const result = await service.install({ scope: 'platform' }, [packageId]);

      expect(sourceInstall).not.toHaveBeenCalled();
      expect(storeResolve).toHaveBeenCalledOnce();
      expect(result.installed[0]?.primaryKind).toBe('plugin');
    });

    it('keeps platform adapters in the platform package graph', async () => {
      const packageId = '@kb-labs/adapters-test';
      const packageRoot = await createAdapterDir(tmpDir, 'platform-adapter', packageId);
      const source: PackageSource = {
        async resolve(): Promise<ResolvedPackage> {
          return { id: packageId, version: '1.0.0', integrity: 'sha256-fixture', source: 'marketplace', primaryKind: 'adapter' };
        },
        async install(): Promise<InstalledPackage> {
          return { id: packageId, version: '1.0.0', packageRoot, integrity: 'sha256-fixture' };
        },
        async remove() {},
      };
      const sourceInstall = vi.spyOn(source, 'install');
      const storeResolve = vi.fn(async () => { throw new Error('Platform adapters must not use the plugin store'); });
      const store: IExtensionStore = {
        root: path.join(tmpDir, 'store'),
        casDir: path.join(tmpDir, 'store', 'cas'),
        entriesDir: path.join(tmpDir, 'store', 'entries'),
        async lookup() { return undefined; },
        resolve: storeResolve,
        async listEntries() { return []; },
      };
      const service = makeMarketplaceService({ platformRoot: tmpDir, source, extensionStore: store });

      const result = await service.install({ scope: 'platform' }, [packageId]);

      expect(sourceInstall).toHaveBeenCalledOnce();
      expect(storeResolve).not.toHaveBeenCalled();
      expect(result.installed[0]?.primaryKind).toBe('adapter');
      const lock = await readMarketplaceLock(tmpDir, new DiagnosticCollector());
      expect(lock?.adapters?.[packageId]?.resolvedPath).toBe('./packages/platform-adapter');
    });

    it('installs multiple plugins in one call', async () => {
      const { dir: dir1, integrity: int1 } = await createPluginDir(tmpDir, 'a', '@kb-labs/a');
      const { dir: dir2, integrity: int2 } = await createPluginDir(tmpDir, 'b', '@kb-labs/b', '2.0.0');
      const source = createMockSource(new Map([
        ['@kb-labs/a', { dir: dir1, version: '1.0.0', integrity: int1 }],
        ['@kb-labs/b', { dir: dir2, version: '2.0.0', integrity: int2 }],
      ]));

      const service = makeMarketplaceService({ platformRoot: tmpDir, source });
      const result = await service.install({ scope: "platform" }, ['@kb-labs/a', '@kb-labs/b']);

      expect(result.installed).toHaveLength(2);
      expect(result.installed.map(e => e.id).sort()).toEqual(['@kb-labs/a', '@kb-labs/b']);

      const lock = await readMarketplaceLock(tmpDir, new DiagnosticCollector());
      expect(Object.keys(lock!.plugins)).toHaveLength(2);
    });

    it('serializes concurrent mutations for the same marketplace scope', async () => {
      const { dir: dir1, integrity: int1 } = await createPluginDir(tmpDir, 'serial-a', '@kb-labs/serial-a');
      const { dir: dir2, integrity: int2 } = await createPluginDir(tmpDir, 'serial-b', '@kb-labs/serial-b');
      const packages = new Map([
        ['@kb-labs/serial-a', { dir: dir1, version: '1.0.0', integrity: int1 }],
        ['@kb-labs/serial-b', { dir: dir2, version: '1.0.0', integrity: int2 }],
      ]);
      const source = createMockSource(packages);
      let active = 0;
      let maxActive = 0;
      const originalInstall = source.install;
      source.install = async (...args) => {
        active++;
        maxActive = Math.max(maxActive, active);
        await new Promise<void>(resolve => {
          setTimeout(resolve, 20);
        });
        try {
          return await originalInstall(...args);
        } finally {
          active--;
        }
      };

      const service = makeMarketplaceService({ platformRoot: tmpDir, source });
      await Promise.all([
        service.install({ scope: 'platform' }, ['@kb-labs/serial-a']),
        service.install({ scope: 'platform' }, ['@kb-labs/serial-b']),
      ]);

      expect(maxActive).toBe(1);
      const lock = await readMarketplaceLock(tmpDir, new DiagnosticCollector());
      expect(Object.keys(lock!.plugins).sort()).toEqual(['@kb-labs/serial-a', '@kb-labs/serial-b']);
    });

    it('catches afterInstall hook errors as warnings', async () => {
      const { dir, integrity } = await createPluginDir(tmpDir, 'hook-fail', '@kb-labs/hook-fail');
      const source = createMockSource(
        new Map([['@kb-labs/hook-fail', { dir, version: '1.0.0', integrity }]]),
      );

      const service = makeMarketplaceService({ platformRoot: tmpDir, source });

      // Register a strategy with a failing afterInstall hook
      service.registerStrategy({
        kind: 'plugin',
        async detectKind() { return 'plugin'; },
        async extractProvides() { return ['plugin']; },
        async afterInstall() { throw new Error('hook-boom'); },
      });

      const result = await service.install({ scope: "platform" }, ['@kb-labs/hook-fail']);

      expect(result.installed).toHaveLength(1);
      expect(result.warnings).toHaveLength(1);
      expect(result.warnings[0]).toContain('hook-boom');
    });

    it('B-021: rejects a package with no detectable KB Labs entity kind', async () => {
      // Installing a plain npm package (no kb manifest) must FAIL LOUDLY,
      // not silently record it as a 'plugin'. This is what happened with
      // `install mind` fetching the unrelated npm `mind` library.
      const { dir, integrity } = await createNonKbPackageDir(tmpDir, 'mind', 'mind');
      const source = createMockSource(
        new Map([['mind', { dir, version: '0.0.2', integrity }]]),
      );

      const service = makeMarketplaceService({ platformRoot: tmpDir, source });

      const { InvalidEntityError } = await import('../marketplace-service.js');
      await expect(
        service.install({ scope: 'platform' }, ['mind']),
      ).rejects.toBeInstanceOf(InvalidEntityError);

      // Lock must NOT contain the bogus entry.
      const lock = await readMarketplaceLock(tmpDir, new DiagnosticCollector());
      expect(lock?.plugins['mind']).toBeUndefined();
    });
  });

  // ── uninstall ────────────────────────────────────────────────────────

  describe('uninstall', () => {
    it('removes plugin from lock and cache', async () => {
      const { dir, integrity } = await createPluginDir(tmpDir, 'to-remove', '@kb-labs/to-remove');
      const removeSpy = vi.fn();
      const source: PackageSource = {
        ...createMockSource(new Map([['@kb-labs/to-remove', { dir, version: '1.0.0', integrity }]])),
        remove: removeSpy,
      };

      const service = makeMarketplaceService({ platformRoot: tmpDir, source });
      await service.install({ scope: "platform" }, ['@kb-labs/to-remove']);

      // Verify installed
      let lock = await readMarketplaceLock(tmpDir, new DiagnosticCollector());
      expect(lock!.plugins['@kb-labs/to-remove']).toBeDefined();

      // Uninstall
      await service.uninstall({ scope: "platform" }, ['@kb-labs/to-remove']);

      lock = await readMarketplaceLock(tmpDir, new DiagnosticCollector());
      expect(lock!.plugins['@kb-labs/to-remove']).toBeUndefined();
      expect(removeSpy).toHaveBeenCalledWith('@kb-labs/to-remove', tmpDir);
    });

    it('uninstalls multiple plugins', async () => {
      const { dir: d1, integrity: i1 } = await createPluginDir(tmpDir, 'x', '@kb-labs/x');
      const { dir: d2, integrity: i2 } = await createPluginDir(tmpDir, 'y', '@kb-labs/y');
      const source = createMockSource(new Map([
        ['@kb-labs/x', { dir: d1, version: '1.0.0', integrity: i1 }],
        ['@kb-labs/y', { dir: d2, version: '1.0.0', integrity: i2 }],
      ]));

      const service = makeMarketplaceService({ platformRoot: tmpDir, source });
      await service.install({ scope: "platform" }, ['@kb-labs/x', '@kb-labs/y']);
      await service.uninstall({ scope: "platform" }, ['@kb-labs/x', '@kb-labs/y']);

      const lock = await readMarketplaceLock(tmpDir, new DiagnosticCollector());
      expect(Object.keys(lock!.plugins)).toHaveLength(0);
    });
  });

  // ── link / unlink ────────────────────────────────────────────────────

  describe('link', () => {
    it('links a local plugin directory', async () => {
      const { dir } = await createPluginDir(tmpDir, 'local-plugin', '@kb-labs/local-plugin');
      const source = createMockSource(new Map());

      const service = makeMarketplaceService({ platformRoot: tmpDir, source });
      const result = await service.link({ scope: "platform" }, path.relative(tmpDir, dir));

      expect(result.id).toBe('@kb-labs/local-plugin');
      expect(result.version).toBe('1.0.0');
      expect(result.primaryKind).toBe('plugin');
      expect(result.provides).toContain('cli-command');

      const lock = await readMarketplaceLock(tmpDir, new DiagnosticCollector());
      expect(lock!.plugins['@kb-labs/local-plugin']).toBeDefined();
      expect(lock!.plugins['@kb-labs/local-plugin']!.source).toBe('local');
    });

    it('links an unbuilt scaffold package from its validated source manifest', async () => {
      const dir = path.join(tmpDir, 'local-plugin-entry');
      await fs.mkdir(dir, { recursive: true });
      await fs.writeFile(
        path.join(dir, 'package.json'),
        JSON.stringify({
          name: '@kb-labs/local-plugin-entry',
          version: '0.1.0',
          kb: { manifest: './dist/manifest.js' },
        }),
      );
      const source = createMockSource(new Map());
      const manifest = {
        schema: 'kb.plugin/3',
        id: '@kb-labs/local-plugin',
        version: '0.1.0',
        display: { name: 'local-plugin', description: 'source manifest' },
        cli: { commands: [{ path: 'local-plugin ping', handler: './ping.js' }] },
      };

      const service = makeMarketplaceService({ platformRoot: tmpDir, source });
      const result = await service.link(
        { scope: 'platform' },
        path.relative(tmpDir, dir),
        manifest,
      );

      expect(result.id).toBe('@kb-labs/local-plugin');
      expect(result.primaryKind).toBe('plugin');
      const lock = await readMarketplaceLock(tmpDir, new DiagnosticCollector());
      expect(lock!.plugins['@kb-labs/local-plugin']).toBeDefined();
      expect(lock!.plugins['@kb-labs/local-plugin-entry']).toBeUndefined();
    });

    it('rejects path outside workspace root (path traversal)', async () => {
      const source = createMockSource(new Map());
      const service = makeMarketplaceService({ platformRoot: tmpDir, source });

      await expect(service.link({ scope: "platform" }, '../../etc/passwd')).rejects.toThrow('outside platform root');
    });
  });

  describe('unlink', () => {
    it('removes linked plugin from lock', async () => {
      const { dir } = await createPluginDir(tmpDir, 'to-unlink', '@kb-labs/to-unlink');
      const source = createMockSource(new Map());

      const service = makeMarketplaceService({ platformRoot: tmpDir, source });
      await service.link({ scope: "platform" }, path.relative(tmpDir, dir));
      await service.unlink({ scope: "platform" }, '@kb-labs/to-unlink');

      const lock = await readMarketplaceLock(tmpDir, new DiagnosticCollector());
      expect(lock!.plugins['@kb-labs/to-unlink']).toBeUndefined();
    });
  });

  // ── enable / disable ─────────────────────────────────────────────────

  describe('enable / disable', () => {
    it('enables a disabled plugin', async () => {
      const { dir, integrity } = await createPluginDir(tmpDir, 'toggle', '@kb-labs/toggle');
      const source = createMockSource(
        new Map([['@kb-labs/toggle', { dir, version: '1.0.0', integrity }]]),
      );

      const service = makeMarketplaceService({ platformRoot: tmpDir, source });
      await service.install({ scope: "platform" }, ['@kb-labs/toggle']);

      await service.disable({ scope: "platform" }, '@kb-labs/toggle');
      let lock = await readMarketplaceLock(tmpDir, new DiagnosticCollector());
      expect(lock!.plugins['@kb-labs/toggle']!.enabled).toBe(false);

      await service.enable({ scope: "platform" }, '@kb-labs/toggle');
      lock = await readMarketplaceLock(tmpDir, new DiagnosticCollector());
      expect(lock!.plugins['@kb-labs/toggle']!.enabled).toBe(true);
    });

    it('throws when enabling a non-existent package', async () => {
      const source = createMockSource(new Map());
      const service = makeMarketplaceService({ platformRoot: tmpDir, source });

      await expect(service.enable({ scope: "platform" }, '@kb-labs/ghost')).rejects.toThrow('not found');
    });

    it('throws when disabling a non-existent package', async () => {
      const source = createMockSource(new Map());
      const service = makeMarketplaceService({ platformRoot: tmpDir, source });

      await expect(service.disable({ scope: "platform" }, '@kb-labs/ghost')).rejects.toThrow('not found');
    });
  });

  // ── list / getEntry ──────────────────────────────────────────────────

  describe('list / getEntry', () => {
    it('lists installed plugins', async () => {
      const { dir, integrity } = await createPluginDir(tmpDir, 'listed', '@kb-labs/listed');
      const source = createMockSource(
        new Map([['@kb-labs/listed', { dir, version: '1.0.0', integrity }]]),
      );

      const service = makeMarketplaceService({ platformRoot: tmpDir, source });
      await service.install({ scope: "platform" }, ['@kb-labs/listed']);

      const list = await service.list({ scope: "platform" });
      expect(list).toHaveLength(1);
      expect(list[0]!.id).toBe('@kb-labs/listed');
    });

    it('filters by kind', async () => {
      const { dir, integrity } = await createPluginDir(tmpDir, 'filter-me', '@kb-labs/filter-me');
      const source = createMockSource(
        new Map([['@kb-labs/filter-me', { dir, version: '1.0.0', integrity }]]),
      );

      const service = makeMarketplaceService({ platformRoot: tmpDir, source });
      await service.install({ scope: "platform" }, ['@kb-labs/filter-me']);

      const plugins = await service.list({ scope: "platform" }, { kind: 'plugin' });
      expect(plugins).toHaveLength(1);

      const adapters = await service.list({ scope: "platform" }, { kind: 'adapter' });
      expect(adapters).toHaveLength(0);
    });

    it('returns empty list when no lock file', async () => {
      const source = createMockSource(new Map());
      const service = makeMarketplaceService({ platformRoot: tmpDir, source });

      const list = await service.list({ scope: "platform" });
      expect(list).toEqual([]);
    });

    it('getEntry returns entry or null', async () => {
      const { dir, integrity } = await createPluginDir(tmpDir, 'entry-get', '@kb-labs/entry-get');
      const source = createMockSource(
        new Map([['@kb-labs/entry-get', { dir, version: '1.0.0', integrity }]]),
      );

      const service = makeMarketplaceService({ platformRoot: tmpDir, source });
      await service.install({ scope: "platform" }, ['@kb-labs/entry-get']);

      const entry = await service.getEntry({ scope: "platform" }, '@kb-labs/entry-get');
      expect(entry).not.toBeNull();
      expect(entry!.version).toBe('1.0.0');

      const missing = await service.getEntry({ scope: "platform" }, '@kb-labs/ghost');
      expect(missing).toBeNull();
    });
  });

  // ── doctor ───────────────────────────────────────────────────────────

  describe('doctor', () => {
    it('returns ok: true when all packages are healthy', async () => {
      const { dir, integrity } = await createPluginDir(tmpDir, 'healthy', '@kb-labs/healthy');
      const source = createMockSource(
        new Map([['@kb-labs/healthy', { dir, version: '1.0.0', integrity }]]),
      );

      const service = makeMarketplaceService({ platformRoot: tmpDir, source });
      await service.install({ scope: "platform" }, ['@kb-labs/healthy']);

      const report = await service.doctor({ scope: "platform" });
      expect(report.ok).toBe(true);
      expect(report.total).toBe(1);
      // Info about missing signature is expected
      const errors = report.issues.filter(i => i.severity === 'error');
      expect(errors).toHaveLength(0);
    });

    it('reports error when package directory is missing', async () => {
      const { dir, integrity } = await createPluginDir(tmpDir, 'will-vanish', '@kb-labs/will-vanish');
      const source = createMockSource(
        new Map([['@kb-labs/will-vanish', { dir, version: '1.0.0', integrity }]]),
      );

      const service = makeMarketplaceService({ platformRoot: tmpDir, source });
      await service.install({ scope: "platform" }, ['@kb-labs/will-vanish']);

      // Delete the package directory
      await fs.rm(dir, { recursive: true, force: true });

      const report = await service.doctor({ scope: "platform" });
      expect(report.ok).toBe(false);
      const error = report.issues.find(i => i.severity === 'error');
      expect(error).toBeDefined();
      expect(error!.message).toContain('not found');
      expect(error!.diagnosticCode).toBe('PACKAGE_NOT_FOUND');
    });

    // case: S-203.24
    it('preserves the policy diagnostic code, config path, and scoped repair command', async () => {
      const { dir, integrity } = await createPluginDir(tmpDir, 'policy-blocked', '@vendor/policy-blocked');
      const source = createMockSource(new Map([['@vendor/policy-blocked', { dir, version: '1.0.0', integrity }]]));
      const projectRoot = path.join(tmpDir, 'project');
      await fs.mkdir(projectRoot, { recursive: true });
      const resolved = await source.resolve('@vendor/policy-blocked');
      const storeEntry = {
        schema: 'kb.store-entry/1' as const,
        name: resolved.id,
        version: resolved.version,
        kind: 'plugin' as const,
        integrity,
        installedAt: new Date().toISOString(),
      };
      const stored: ResolvedStoreEntry = { entry: storeEntry, packageRoot: dir, entryDir: path.join(tmpDir, 'store', 'entries', 'policy-blocked') };
      const store: IExtensionStore = {
        root: path.join(tmpDir, 'store'),
        casDir: path.join(tmpDir, 'store', 'cas'),
        entriesDir: path.join(tmpDir, 'store', 'entries'),
        async lookup() { return stored; },
        async resolve() { return stored; },
        async listEntries() { return [storeEntry]; },
      };
      const service = makeMarketplaceService({ platformRoot: tmpDir, projectRoot, source, extensionStore: store });
      await service.install({ scope: 'project', projectRoot }, ['@vendor/policy-blocked']);
      const configPath = path.join(projectRoot, '.kb', 'kb.config.json');
      await fs.writeFile(configPath, JSON.stringify({ plugins: { block: 7 } }));

      const report = await service.doctor({ scope: 'project', projectRoot });

      expect(report.ok).toBe(false);
      expect(report.issues).toContainEqual(expect.objectContaining({
        packageId: '@vendor/policy-blocked',
        diagnosticCode: 'PLUGIN_POLICY_TYPE_INVALID',
        path: configPath,
        message: expect.stringContaining('plugins.block must be an array'),
        remediation: expect.stringContaining('kb marketplace plugins doctor --scope project'),
      }));
    });

    // case: S-203.21
    it('reports discovery failures for enabled plugin entries with their diagnostic code', async () => {
      const { dir, integrity } = await createPluginDir(tmpDir, 'id-mismatch', '@kb-labs/id-mismatch');
      const source = createMockSource(new Map([['@kb-labs/id-mismatch', { dir, version: '1.0.0', integrity }]]));
      const service = makeMarketplaceService({ platformRoot: tmpDir, source });
      await service.install({ scope: 'platform' }, ['@kb-labs/id-mismatch']);
      await fs.writeFile(path.join(dir, 'kb.plugin.json'), JSON.stringify({
        schema: 'kb.plugin/3', id: '@kb-labs/other-id', version: '1.0.0',
      }));

      const report = await service.doctor({ scope: 'platform' });
      expect(report.ok).toBe(false);
      expect(report.issues).toContainEqual(expect.objectContaining({
        packageId: '@kb-labs/id-mismatch',
        severity: 'error',
        diagnosticCode: 'PLUGIN_ID_MISMATCH',
      }));
    });

    // case: S-203.21
    it('lists enabled plugins with missing command handlers and their discovery code', async () => {
      const { dir, integrity } = await createPluginDir(tmpDir, 'missing-handler', '@kb-labs/missing-handler');
      const source = createMockSource(new Map([['@kb-labs/missing-handler', { dir, version: '1.0.0', integrity }]]));
      const service = makeMarketplaceService({ platformRoot: tmpDir, source });
      await service.install({ scope: 'platform' }, ['@kb-labs/missing-handler']);
      await fs.rm(path.join(dir, 'dist', 'h.js'));

      const report = await service.doctor({ scope: 'platform' });
      expect(report.ok).toBe(false);
      expect(report.issues).toContainEqual(expect.objectContaining({
        packageId: '@kb-labs/missing-handler',
        severity: 'error',
        diagnosticCode: 'COMMAND_HANDLER_MISSING',
        remediation: expect.stringContaining('kb marketplace plugins doctor'),
      }));
    });

    // case: S-203.21
    it('lists enabled plugins with invalid command schemas and their discovery code', async () => {
      const { dir, integrity } = await createPluginDir(tmpDir, 'invalid-command-schema', '@kb-labs/invalid-command-schema');
      const source = createMockSource(new Map([['@kb-labs/invalid-command-schema', { dir, version: '1.0.0', integrity }]]));
      const service = makeMarketplaceService({ platformRoot: tmpDir, source });
      await service.install({ scope: 'platform' }, ['@kb-labs/invalid-command-schema']);
      await fs.writeFile(path.join(dir, 'kb.plugin.json'), JSON.stringify({
        schema: 'kb.plugin/3',
        id: '@kb-labs/invalid-command-schema',
        version: '1.0.0',
        cli: { commands: [{ path: 'invalid run', describe: 'Invalid flags', handler: './h.js', flags: [{ name: 'runId', type: 'string' }] }] },
      }));

      const report = await service.doctor({ scope: 'platform' });
      expect(report.ok).toBe(false);
      expect(report.issues).toContainEqual(expect.objectContaining({
        packageId: '@kb-labs/invalid-command-schema',
        severity: 'error',
        diagnosticCode: 'COMMAND_SCHEMA_INVALID',
      }));
    });

    it('warns about integrity mismatch', async () => {
      const { dir, integrity } = await createPluginDir(tmpDir, 'tampered', '@kb-labs/tampered');
      const source = createMockSource(
        new Map([['@kb-labs/tampered', { dir, version: '1.0.0', integrity }]]),
      );

      const service = makeMarketplaceService({ platformRoot: tmpDir, source });
      await service.install({ scope: "platform" }, ['@kb-labs/tampered']);

      // Tamper with package.json
      await fs.writeFile(
        path.join(dir, 'package.json'),
        JSON.stringify({ name: '@kb-labs/tampered', version: '1.0.0', tampered: true }),
      );

      const report = await service.doctor({ scope: "platform" });
      const warning = report.issues.find(i => i.severity === 'warning' && i.message.includes('Integrity'));
      expect(warning).toBeDefined();
    });

    it('reports info about unsigned packages', async () => {
      const { dir, integrity } = await createPluginDir(tmpDir, 'unsigned', '@kb-labs/unsigned');
      const source = createMockSource(
        new Map([['@kb-labs/unsigned', { dir, version: '1.0.0', integrity }]]),
      );

      const service = makeMarketplaceService({ platformRoot: tmpDir, source });
      await service.install({ scope: "platform" }, ['@kb-labs/unsigned']);

      const report = await service.doctor({ scope: "platform" });
      const info = report.issues.find(i => i.severity === 'info' && i.message.includes('not signed'));
      expect(info).toBeDefined();
    });

    it('returns ok: true with empty lock', async () => {
      const source = createMockSource(new Map());
      const service = makeMarketplaceService({ platformRoot: tmpDir, source });

      const report = await service.doctor({ scope: "platform" });
      expect(report.ok).toBe(true);
    });
  });

  // ── sync ─────────────────────────────────────────────────────────────

  describe('sync', () => {
    it('discovers plugins from glob patterns', async () => {
      await createPluginDir(tmpDir, 'found-plugin', '@kb-labs/found-plugin');
      const source = createMockSource(new Map());

      const service = makeMarketplaceService({ platformRoot: tmpDir, source });
      const result = await service.sync({ scope: "platform" }, {
        include: ['packages/*'],
      });

      expect(result.added).toHaveLength(1);
      expect(result.added[0]!.id).toBe('@kb-labs/found-plugin');
      expect(result.total).toBe(1);

      // Verify lock file updated
      const lock = await readMarketplaceLock(tmpDir, new DiagnosticCollector());
      expect(lock!.plugins['@kb-labs/found-plugin']).toBeDefined();
    });

    // case: S-179.4
    it('records workspace adapters in the platform adapters lock section', async () => {
      const adapterDir = await createAdapterDir(tmpDir, 'found-adapter', '@kb-labs/found-adapter');
      const service = makeMarketplaceService({ platformRoot: tmpDir, source: createMockSource(new Map()) });

      const result = await service.sync({ scope: 'platform' }, { include: ['packages/*'] });

      expect(result.added).toContainEqual({ id: '@kb-labs/found-adapter', primaryKind: 'adapter', version: '1.0.0' });
      const lock = await readMarketplaceLock(tmpDir, new DiagnosticCollector());
      expect(lock?.adapters?.['@kb-labs/found-adapter']).toMatchObject({
        source: 'platform',
        resolvedPath: `./${path.relative(tmpDir, adapterDir)}`,
      });
      expect(lock?.plugins['@kb-labs/found-adapter']).toBeUndefined();
      expect((await service.list({ scope: 'platform' }, { kind: 'adapter' })).map(entry => entry.id))
        .toContain('@kb-labs/found-adapter');
    });

    it('skips packages already in lock', async () => {
      const { dir, integrity } = await createPluginDir(tmpDir, 'existing', '@kb-labs/existing');
      const source = createMockSource(
        new Map([['@kb-labs/existing', { dir, version: '1.0.0', integrity }]]),
      );

      const service = makeMarketplaceService({ platformRoot: tmpDir, source });
      await service.install({ scope: "platform" }, ['@kb-labs/existing']);

      const result = await service.sync({ scope: "platform" }, { include: ['packages/*'] });
      expect(result.added).toHaveLength(0);
      expect(result.skipped).toHaveLength(1);
      expect(result.skipped[0]!.reason).toContain('already in lock');
    });

    it('marks new entries as disabled by default', async () => {
      await createPluginDir(tmpDir, 'auto-disabled', '@kb-labs/auto-disabled');
      const source = createMockSource(new Map());

      const service = makeMarketplaceService({ platformRoot: tmpDir, source });
      await service.sync({ scope: "platform" }, { include: ['packages/*'], autoEnable: false });

      const lock = await readMarketplaceLock(tmpDir, new DiagnosticCollector());
      expect(lock!.plugins['@kb-labs/auto-disabled']!.enabled).toBe(false);
    });

    it('auto-enables entries when autoEnable is true', async () => {
      await createPluginDir(tmpDir, 'auto-enabled', '@kb-labs/auto-enabled');
      const source = createMockSource(new Map());

      const service = makeMarketplaceService({ platformRoot: tmpDir, source });
      await service.sync({ scope: "platform" }, { include: ['packages/*'], autoEnable: true });

      const lock = await readMarketplaceLock(tmpDir, new DiagnosticCollector());
      expect(lock!.plugins['@kb-labs/auto-enabled']!.enabled).toBe(true);
    });
  });
});

// ---------------------------------------------------------------------------
// mergeScopedEntries — project wins over platform
// ---------------------------------------------------------------------------

function makeEntry(id: string, version: string): MarketplaceEntryWithId {
  return {
    id,
    version,
    resolvedPath: `./node_modules/${id}`,
    source: 'marketplace',
    package: id,
    primaryKind: 'plugin',
    provides: ['plugin'],
    integrity: 'sha256-test',
  };
}

describe('mergeScopedEntries — project wins', () => {
  it('returns platform-only entries unchanged', () => {
    const { entries, diagnostics } = mergeScopedEntries([
      { scope: 'platform', entries: [makeEntry('@kb/a', '1.0.0')] },
    ]);
    expect(entries).toHaveLength(1);
    expect(entries[0]!.id).toBe('@kb/a');
    expect(entries[0]!.scope).toBe('platform');
    expect(diagnostics).toHaveLength(0);
  });

  it('returns project-only entries unchanged', () => {
    const { entries, diagnostics } = mergeScopedEntries([
      { scope: 'project', entries: [makeEntry('@kb/b', '2.0.0')] },
    ]);
    expect(entries).toHaveLength(1);
    expect(entries[0]!.scope).toBe('project');
    expect(diagnostics).toHaveLength(0);
  });

  it('merges non-overlapping entries from both scopes', () => {
    const { entries, diagnostics } = mergeScopedEntries([
      { scope: 'platform', entries: [makeEntry('@kb/platform-only', '1.0.0')] },
      { scope: 'project',  entries: [makeEntry('@kb/project-only',  '2.0.0')] },
    ]);
    expect(entries).toHaveLength(2);
    expect(diagnostics).toHaveLength(0);
  });

  it('project entry wins on collision and emits diagnostic', () => {
    const { entries, diagnostics } = mergeScopedEntries([
      { scope: 'platform', entries: [makeEntry('@kb/shared', '1.0.0')] },
      { scope: 'project',  entries: [makeEntry('@kb/shared', '2.0.0')] },
    ]);

    expect(entries).toHaveLength(1);
    const winner = entries[0]!;
    expect(winner.id).toBe('@kb/shared');
    expect(winner.version).toBe('2.0.0');    // project version
    expect(winner.scope).toBe('project');

    expect(diagnostics).toHaveLength(1);
    expect(diagnostics[0]!.code).toBe('MARKETPLACE_SCOPE_COLLISION');
    expect(diagnostics[0]!.message).toContain('project wins');
  });

  it('project wins regardless of input order', () => {
    // Project entries listed before platform
    const { entries: r1 } = mergeScopedEntries([
      { scope: 'project',  entries: [makeEntry('@kb/shared', '2.0.0')] },
      { scope: 'platform', entries: [makeEntry('@kb/shared', '1.0.0')] },
    ]);
    expect(r1[0]!.version).toBe('2.0.0');
    expect(r1[0]!.scope).toBe('project');

    // Platform entries listed before project
    const { entries: r2 } = mergeScopedEntries([
      { scope: 'platform', entries: [makeEntry('@kb/shared', '1.0.0')] },
      { scope: 'project',  entries: [makeEntry('@kb/shared', '2.0.0')] },
    ]);
    expect(r2[0]!.version).toBe('2.0.0');
    expect(r2[0]!.scope).toBe('project');
  });
});
