/**
 * Integration test: marketplace install → discovery → plugin available
 *
 * Full lifecycle:
 *   1. MarketplaceService.install() writes lock + cache
 *   2. DiscoveryManager.discover() reads lock + loads manifests
 *   3. Plugin is available with correct entity kinds
 *
 * No mocking of discovery or marketplace internals — real filesystem.
 */
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import * as fs from 'node:fs/promises';
import * as path from 'node:path';
import * as os from 'node:os';
import * as crypto from 'node:crypto';
import { DiscoveryManager } from '@kb-labs/core-discovery';
import { createExtensionStore } from '@kb-labs/core-extension-store';
import type { PackageSource, InstalledPackage, ResolvedPackage } from '@kb-labs/marketplace-contracts';
import { MarketplaceService } from '../marketplace-service.js';

describe('Marketplace → Discovery Integration', () => {
  let tmpDir: string;

  beforeEach(async () => {
    tmpDir = await fs.mkdtemp(path.join(os.tmpdir(), 'kb-mkt-disc-'));
  });

  afterEach(async () => {
    await fs.rm(tmpDir, { recursive: true, force: true });
  });

  function computeIntegrity(content: string): string {
    return `sha256-${crypto.createHash('sha256').update(Buffer.from(content)).digest('base64')}`;
  }

  async function createPluginOnDisk(name: string, id: string, version: string) {
    const dir = path.join(tmpDir, 'packages', name);
    await fs.mkdir(dir, { recursive: true });

    const pkgJson = JSON.stringify({ name: id, version });
    await fs.writeFile(path.join(dir, 'package.json'), pkgJson);
    await fs.writeFile(
      path.join(dir, 'kb.plugin.json'),
      JSON.stringify({
        schema: 'kb.plugin/3',
        id,
        version,
        display: { name, description: `${name} plugin` },
        cli: { commands: [{ path: `${name} run`, describe: `Run ${name}`, handler: './dist/h.js' }] },
      }),
    );

    return { dir, integrity: computeIntegrity(pkgJson) };
  }

  it('install → discover: plugin is available after marketplace install', async () => {
    const { dir, integrity } = await createPluginOnDisk('review', '@kb-labs/review', '2.0.0');

    // Step 1: Install via MarketplaceService
    const source: PackageSource = {
      async resolve(spec: string): Promise<ResolvedPackage> {
        return { id: spec, version: '2.0.0', integrity, source: 'marketplace', primaryKind: 'plugin' };
      },
      async install(pkg: ResolvedPackage): Promise<InstalledPackage> {
        return { id: pkg.id, version: pkg.version, packageRoot: dir, integrity: pkg.integrity };
      },
      async remove() {},
    };

    const marketplace = new MarketplaceService({ platformRoot: tmpDir, source });
    const installResult = await marketplace.install({ scope: "platform" }, ['@kb-labs/review']);

    expect(installResult.installed).toHaveLength(1);
    expect(installResult.installed[0]!.id).toBe('@kb-labs/review');
    expect(installResult.installed[0]!.provides).toContain('cli-command');

    // Step 2: Discover via DiscoveryManager
    const dm = new DiscoveryManager({ root: tmpDir, verifyIntegrity: false });
    const discoverResult = await dm.discover();

    expect(discoverResult.plugins).toHaveLength(1);
    expect(discoverResult.plugins[0]!.id).toBe('@kb-labs/review');
    expect(discoverResult.plugins[0]!.version).toBe('2.0.0');
    expect(discoverResult.plugins[0]!.provides).toContain('cli-command');

    // Manifest should be fully loaded
    const manifest = discoverResult.manifests.get('@kb-labs/review');
    expect(manifest).toBeDefined();
    expect(manifest!.cli?.commands).toHaveLength(1);
    expect(manifest!.cli?.commands?.[0]?.path).toBe('review run');
  });

  it('writes a portable v3 pin and resolves it from two independent machine stores', async () => {
    const projectRoot = path.join(tmpDir, 'project');
    const storeOneRoot = path.join(tmpDir, 'machine-one');
    const storeTwoRoot = path.join(tmpDir, 'machine-two');
    await fs.mkdir(path.join(projectRoot, '.kb'), { recursive: true });
    await fs.writeFile(path.join(projectRoot, '.kb', 'kb.config.json'), '{}');
    const { dir } = await createPluginOnDisk('portable', '@kb-labs/portable', '1.0.0');
    const integrity = 'sha512-cG9ydGFibGU=';
    const installer = {
      async install(options: { name: string; version: string; projectDir: string }): Promise<{ packageRoot: string; integrity: string }> {
        const packageRoot = path.join(options.projectDir, 'node_modules', ...options.name.split('/'));
        await fs.mkdir(path.dirname(packageRoot), { recursive: true });
        await fs.cp(dir, packageRoot, { recursive: true });
        return { packageRoot, integrity };
      },
    };
    const source: PackageSource = {
      async resolve(spec: string): Promise<ResolvedPackage> {
        return { id: spec, version: '1.0.0', integrity, source: 'marketplace', primaryKind: 'plugin', downloadUrl: `file:${dir}` };
      },
      async install(): Promise<InstalledPackage> { throw new Error('store install should be used'); },
      async remove() {},
    };
    const storeOne = createExtensionStore({ root: storeOneRoot, installer });
    const marketplace = new MarketplaceService({ platformRoot: tmpDir, projectRoot, source, extensionStore: storeOne });
    await marketplace.install({ scope: 'project' }, ['@kb-labs/portable']);

    const lockFile = path.join(projectRoot, '.kb', 'marketplace.lock');
    const lockText = await fs.readFile(lockFile, 'utf8');
    const lock = JSON.parse(lockText);
    expect(lock.schema).toBe('kb.marketplace/3');
    expect(lock.plugins['@kb-labs/portable']).toMatchObject({ source: 'marketplace', version: '1.0.0', enabled: true });
    expect(lock.plugins['@kb-labs/portable'].integrity).toMatch(/^sha(256|512)-/);
    expect(lockText).not.toMatch(/resolvedPath|installedAt|machine-one|\/Users\/|[A-Z]:\\/);

    const pin = lock.plugins['@kb-labs/portable'];
    const storeTwo = createExtensionStore({ root: storeTwoRoot, installer });
    await storeTwo.resolve({ name: '@kb-labs/portable', version: pin.version, kind: 'plugin', spec: `file:${dir}`, expectedIntegrity: pin.integrity });
    const first = await new DiscoveryManager({ root: projectRoot, platformRoot: tmpDir, extensionStore: storeOne, workspace: false }).discover();
    const second = await new DiscoveryManager({ root: projectRoot, platformRoot: tmpDir, extensionStore: storeTwo, workspace: false }).discover();
    expect(first.plugins.map((plugin) => plugin.id)).toEqual(['@kb-labs/portable']);
    expect(second.plugins.map((plugin) => plugin.id)).toEqual(['@kb-labs/portable']);
    expect(first.plugins[0]?.packageRoot).not.toBe(second.plugins[0]?.packageRoot);
  }, 60_000);

  it('installs platform plugin entries into the shared store and discovers them without a platform graph install', async () => {
    const { dir, integrity } = await createPluginOnDisk('platform-entry', '@kb-labs/platform-entry', '1.0.0');
    const installer = {
      async install(options: { name: string; projectDir: string }): Promise<{ packageRoot: string; integrity: string }> {
        const packageRoot = path.join(options.projectDir, 'node_modules', ...options.name.split('/'));
        await fs.mkdir(path.dirname(packageRoot), { recursive: true });
        await fs.cp(dir, packageRoot, { recursive: true });
        return { packageRoot, integrity };
      },
    };
    const source: PackageSource = {
      async resolve(spec: string): Promise<ResolvedPackage> {
        return { id: spec, version: '1.0.0', integrity, source: 'marketplace', primaryKind: 'plugin' };
      },
      async install(): Promise<InstalledPackage> { throw new Error('platform plugin must use the extension store'); },
      async remove() {},
    };
    const store = createExtensionStore({ root: path.join(tmpDir, 'machine-store'), installer });
    const marketplace = new MarketplaceService({ platformRoot: tmpDir, source, extensionStore: store });

    await marketplace.install({ scope: 'platform' }, ['@kb-labs/platform-entry']);

    const lock = JSON.parse(await fs.readFile(path.join(tmpDir, '.kb', 'marketplace.lock'), 'utf8'));
    expect(lock.plugins['@kb-labs/platform-entry']).toMatchObject({
      source: 'marketplace',
      version: '1.0.0',
      enabled: true,
    });
    await expect(fs.access(path.join(tmpDir, 'node_modules', '@kb-labs', 'platform-entry'))).rejects.toThrow();
    const discovered = await new DiscoveryManager({
      root: tmpDir,
      extensionStore: store,
      workspace: false,
      verifyIntegrity: false,
    }).discover();
    expect(discovered.plugins.map((plugin) => plugin.id)).toEqual(['@kb-labs/platform-entry']);
  });

  it('install → uninstall → discover: plugin is gone after uninstall', async () => {
    const { dir, integrity } = await createPluginOnDisk('temp', '@kb-labs/temp', '1.0.0');

    const source: PackageSource = {
      async resolve(spec: string): Promise<ResolvedPackage> {
        return { id: spec, version: '1.0.0', integrity, source: 'marketplace', primaryKind: 'plugin' };
      },
      async install(pkg: ResolvedPackage): Promise<InstalledPackage> {
        return { id: pkg.id, version: pkg.version, packageRoot: dir, integrity: pkg.integrity };
      },
      async remove() {},
    };

    const marketplace = new MarketplaceService({ platformRoot: tmpDir, source });
    await marketplace.install({ scope: "platform" }, ['@kb-labs/temp']);

    // Verify installed
    let dm = new DiscoveryManager({ root: tmpDir, verifyIntegrity: false });
    let result = await dm.discover();
    expect(result.plugins).toHaveLength(1);

    // Uninstall
    await marketplace.uninstall({ scope: "platform" }, ['@kb-labs/temp']);

    // Verify gone
    dm = new DiscoveryManager({ root: tmpDir, verifyIntegrity: false });
    result = await dm.discover();
    expect(result.plugins).toHaveLength(0);
  });

  it('install → disable → discover: disabled plugin not found', async () => {
    const { dir, integrity } = await createPluginOnDisk('optional', '@kb-labs/optional', '1.0.0');

    const source: PackageSource = {
      async resolve(spec: string): Promise<ResolvedPackage> {
        return { id: spec, version: '1.0.0', integrity, source: 'marketplace', primaryKind: 'plugin' };
      },
      async install(pkg: ResolvedPackage): Promise<InstalledPackage> {
        return { id: pkg.id, version: pkg.version, packageRoot: dir, integrity: pkg.integrity };
      },
      async remove() {},
    };

    const marketplace = new MarketplaceService({ platformRoot: tmpDir, source });
    await marketplace.install({ scope: "platform" }, ['@kb-labs/optional']);

    // Disable
    await marketplace.disable({ scope: "platform" }, '@kb-labs/optional');

    // Discovery should skip
    const dm = new DiscoveryManager({ root: tmpDir, verifyIntegrity: false });
    const result = await dm.discover();
    expect(result.plugins).toHaveLength(0);

    const disabled = result.diagnostics.find(d => d.code === 'PLUGIN_DISABLED');
    expect(disabled).toBeDefined();

    // Re-enable
    await marketplace.enable({ scope: "platform" }, '@kb-labs/optional');

    const result2 = await dm.discover();
    expect(result2.plugins).toHaveLength(1);
  });

  it('link → discover: local linked plugin is available', async () => {
    await createPluginOnDisk('local-dev', '@kb-labs/local-dev', '0.0.1');

    const source: PackageSource = {
      async resolve(): Promise<ResolvedPackage> { throw new Error('unused'); },
      async install(): Promise<InstalledPackage> { throw new Error('unused'); },
      async remove() {},
    };

    const marketplace = new MarketplaceService({ platformRoot: tmpDir, source });
    const linkResult = await marketplace.link({ scope: "platform" }, 'packages/local-dev');

    expect(linkResult.id).toBe('@kb-labs/local-dev');
    expect(linkResult.primaryKind).toBe('plugin');

    // Discovery should find linked plugin
    const dm = new DiscoveryManager({ root: tmpDir, verifyIntegrity: true });
    const result = await dm.discover();

    expect(result.plugins).toHaveLength(1);
    expect(result.plugins[0]!.id).toBe('@kb-labs/local-dev');
    expect(result.plugins[0]!.source.kind).toBe('local');
  });
});
