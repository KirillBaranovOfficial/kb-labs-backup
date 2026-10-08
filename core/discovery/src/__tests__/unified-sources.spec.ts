/**
 * The single discovery pipeline: lock entries of both scopes plus workspace
 * development sources, with scope/origin shadowing and static manifest loading.
 */

import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import * as fs from 'node:fs/promises';
import * as path from 'node:path';
import * as os from 'node:os';
import { DiscoveryManager } from '../discovery-manager.js';
import { writeMarketplaceLock, createEmptyLock, createMarketplaceEntry } from '../marketplace-lock.js';
import { computePackageIntegrity } from '../integrity.js';
import { findWorkspaceCandidates, findWorkspacePackages } from '../workspace-source.js';
import { loadAdapterManifest } from '@kb-labs/core-platform';

interface PluginFixture {
  dir: string;
  id: string;
  version?: string;
  /** Emit dist/manifest.json (static). Default true. */
  staticJson?: boolean;
  /** Emit a dist/manifest.js that throws when imported. Default false. */
  throwingModule?: boolean;
  /** Emit a dist/manifest.js exporting a valid manifest. Default false. */
  workingModule?: boolean;
  /** Manifest schema. Default kb.plugin/3. */
  schema?: string;
}

async function makePlugin(f: PluginFixture): Promise<string> {
  const version = f.version ?? '1.0.0';
  await fs.mkdir(path.join(f.dir, 'dist'), { recursive: true });
  await fs.writeFile(
    path.join(f.dir, 'package.json'),
    JSON.stringify({ name: f.id, version, kb: { manifest: './dist/manifest.js' } }),
  );
  const manifest = {
    schema: f.schema ?? 'kb.plugin/3',
    id: f.id,
    version,
    cli: { commands: [{ path: `${f.id.split('/').pop()} hello`, describe: 'Say hello', handler: './dist/hello.js' }] },
  };
  if (f.staticJson ?? true) {
    await fs.writeFile(path.join(f.dir, 'dist', 'manifest.json'), JSON.stringify(manifest));
  }
  if (f.throwingModule) {
    await fs.writeFile(path.join(f.dir, 'dist', 'manifest.js'), 'throw new Error("plugin code must not run");\n');
  }
  if (f.workingModule) {
    await fs.writeFile(path.join(f.dir, 'dist', 'manifest.js'), `export const manifest = ${JSON.stringify(manifest)};\n`);
  }
  return f.dir;
}

async function lockEntry(
  root: string,
  dir: string,
  source: 'platform' | 'local',
  enabled = true,
): Promise<ReturnType<typeof createMarketplaceEntry>> {
  const entryOptions = {
    version: '1.0.0',
    integrity: await computePackageIntegrity(dir),
    resolvedPath: path.relative(root, dir) || '.',
    primaryKind: 'plugin',
    provides: ['plugin'],
  };
  const packageName = JSON.parse(await fs.readFile(path.join(dir, 'package.json'), 'utf8')).name as string;
  const entry = source === 'local'
    ? createMarketplaceEntry({ ...entryOptions, source: 'local' })
    : createMarketplaceEntry({ ...entryOptions, source: 'platform', package: packageName });
  entry.enabled = enabled;
  return entry;
}

async function writeLock(
  root: string,
  entries: Record<string, ReturnType<typeof createMarketplaceEntry>>,
): Promise<void> {
  const lock = createEmptyLock();
  lock.plugins = entries;
  await writeMarketplaceLock(root, lock);
}

describe('DiscoveryManager — one pipeline over locks and workspace', () => {
  let platform: string;
  let project: string;
  let outside: string;

  beforeEach(async () => {
    platform = await fs.mkdtemp(path.join(os.tmpdir(), 'kb-unified-platform-'));
    project = await fs.mkdtemp(path.join(os.tmpdir(), 'kb-unified-project-'));
    outside = await fs.mkdtemp(path.join(os.tmpdir(), 'kb-unified-outside-'));
  });

  afterEach(async () => {
    await fs.rm(platform, { recursive: true, force: true });
    await fs.rm(project, { recursive: true, force: true });
    await fs.rm(outside, { recursive: true, force: true });
  });

  it('reports scope and origin for each source', async () => {
    const platformPlugin = await makePlugin({ dir: path.join(platform, 'node_modules', 'plat'), id: '@x/plat' });
    const linkedPlugin = await makePlugin({ dir: path.join(outside, 'linked'), id: '@x/linked' });
    const projectPlugin = await makePlugin({ dir: path.join(project, 'node_modules', 'proj'), id: '@x/proj' });
    await makePlugin({ dir: path.join(platform, 'packages', 'ws'), id: '@x/ws' });
    await fs.writeFile(path.join(platform, 'pnpm-workspace.yaml'), 'packages:\n  - "packages/*"\n');

    await writeLock(platform, {
      '@x/plat': await lockEntry(platform, platformPlugin, 'platform'),
      '@x/linked': await lockEntry(platform, linkedPlugin, 'local'),
    });
    await writeLock(project, { '@x/proj': await lockEntry(project, projectPlugin, 'platform') });

    const result = await new DiscoveryManager({ root: project, platformRoot: platform }).discover();
    const byId = new Map(result.plugins.map(p => [p.id, p]));

    expect([...byId.keys()].sort()).toEqual(['@x/linked', '@x/plat', '@x/proj', '@x/ws']);
    expect(byId.get('@x/plat')).toMatchObject({ scope: 'platform', origin: 'node_modules', manifestKind: 'static' });
    expect(byId.get('@x/linked')).toMatchObject({ scope: 'platform', origin: 'linked' });
    expect(byId.get('@x/proj')).toMatchObject({ scope: 'project', origin: 'node_modules' });
    expect(byId.get('@x/ws')).toMatchObject({ scope: 'platform', origin: 'workspace', packageName: '@x/ws' });
    expect(result.manifests.size).toBe(4);
  });

  it('reads the static manifest.json and never imports the compiled module', async () => {
    const dir = await makePlugin({ dir: path.join(project, 'node_modules', 'safe'), id: '@x/safe', throwingModule: true });
    await writeLock(project, { '@x/safe': await lockEntry(project, dir, 'platform') });

    const result = await new DiscoveryManager({ root: project }).discover();

    expect(result.plugins.map(p => p.id)).toEqual(['@x/safe']);
    expect(result.plugins[0]!.manifestKind).toBe('static');
    expect(result.plugins[0]!.manifestPath).toBe(path.join(dir, 'dist', 'manifest.json'));
    expect(result.failures).toEqual([]);
  });

  it('falls back to importing the compiled module when there is no static manifest', async () => {
    const dir = await makePlugin({ dir: path.join(project, 'node_modules', 'js'), id: '@x/js', staticJson: false, workingModule: true });
    await writeLock(project, { '@x/js': await lockEntry(project, dir, 'platform') });

    const result = await new DiscoveryManager({ root: project }).discover();

    expect(result.plugins.map(p => p.id)).toEqual(['@x/js']);
    expect(result.plugins[0]!.manifestKind).toBe('module');
  });

  it('does not find a package that is in node_modules but in no lock', async () => {
    await makePlugin({ dir: path.join(platform, 'node_modules', 'stray'), id: '@x/stray' });

    const result = await new DiscoveryManager({ root: project, platformRoot: platform }).discover();

    expect(result.plugins).toEqual([]);
  });

  it('project scope wins over platform scope for the same plugin id, even against a workspace package', async () => {
    const platformCopy = await makePlugin({ dir: path.join(platform, 'node_modules', 'dup'), id: '@x/dup', version: '1.0.0' });
    const projectCopy = await makePlugin({ dir: path.join(project, 'node_modules', 'dup'), id: '@x/dup', version: '2.0.0' });
    await makePlugin({ dir: path.join(platform, 'packages', 'dup'), id: '@x/dup', version: '3.0.0' });
    await fs.writeFile(path.join(platform, 'pnpm-workspace.yaml'), 'packages:\n  - "packages/*"\n');
    await writeLock(platform, { '@x/dup': await lockEntry(platform, platformCopy, 'platform') });
    await writeLock(project, { '@x/dup': await lockEntry(project, projectCopy, 'platform') });

    const result = await new DiscoveryManager({ root: project, platformRoot: platform }).discover();

    expect(result.plugins).toHaveLength(1);
    expect(result.plugins[0]).toMatchObject({ id: '@x/dup', version: '2.0.0', scope: 'project' });
  });

  it('inside one scope workspace > linked > node_modules', async () => {
    const linked = await makePlugin({ dir: path.join(platform, 'linked', 'dup'), id: '@x/dup', version: '2.0.0' });
    await writeLock(platform, {
      '@x/dup': await lockEntry(platform, linked, 'local'),
    });

    const withoutWorkspace = await new DiscoveryManager({ root: platform }).discover();
    expect(withoutWorkspace.plugins).toHaveLength(1);
    expect(withoutWorkspace.plugins[0]).toMatchObject({ version: '2.0.0', origin: 'linked' });

    await makePlugin({ dir: path.join(platform, 'packages', 'dup'), id: '@x/dup', version: '3.0.0' });
    await fs.writeFile(path.join(platform, 'pnpm-workspace.yaml'), 'packages:\n  - "packages/*"\n');

    const withWorkspace = await new DiscoveryManager({ root: platform }).discover();
    expect(withWorkspace.plugins).toHaveLength(1);
    expect(withWorkspace.plugins[0]).toMatchObject({ version: '3.0.0', origin: 'workspace' });
  });

  it('a disabled lock entry is skipped', async () => {
    const dir = await makePlugin({ dir: path.join(project, 'node_modules', 'off'), id: '@x/off' });
    await writeLock(project, { '@x/off': await lockEntry(project, dir, 'platform', false) });

    const result = await new DiscoveryManager({ root: project }).discover();

    expect(result.plugins).toEqual([]);
    expect(result.diagnostics.some(d => d.code === 'PLUGIN_DISABLED')).toBe(true);
  });

  it('ignores manifests of another kind silently and records a broken manifest as a failure', async () => {
    await makePlugin({ dir: path.join(platform, 'packages', 'svc'), id: '@x/svc', schema: 'kb.service/1' });
    const broken = path.join(platform, 'packages', 'broken');
    await fs.mkdir(path.join(broken, 'dist'), { recursive: true });
    await fs.writeFile(path.join(broken, 'package.json'), JSON.stringify({ name: '@x/broken', version: '1.0.0', kb: { manifest: './dist/manifest.js' } }));
    await fs.writeFile(path.join(broken, 'dist', 'manifest.js'), 'throw new Error("boom");\n');
    await fs.writeFile(path.join(platform, 'pnpm-workspace.yaml'), 'packages:\n  - "packages/*"\n');

    const result = await new DiscoveryManager({ root: platform }).discover();

    expect(result.plugins).toEqual([]);
    expect(result.failures).toHaveLength(1);
    expect(result.failures[0]).toMatchObject({
      packageName: '@x/broken',
      reason: 'manifest',
      origin: 'workspace',
      manifestPath: path.join(broken, 'dist', 'manifest.js'),
    });
    expect(result.diagnostics.find(d => d.code === 'MANIFEST_NOT_PLUGIN')?.severity).toBe('info');
  });

  it('workspace discovery can be switched off', async () => {
    await makePlugin({ dir: path.join(platform, 'packages', 'ws'), id: '@x/ws' });
    await fs.writeFile(path.join(platform, 'pnpm-workspace.yaml'), 'packages:\n  - "packages/*"\n');

    const result = await new DiscoveryManager({ root: platform, workspace: false }).discover();

    expect(result.plugins).toEqual([]);
  });
});

describe('plugins.allow / plugins.block / plugins.linked gate', () => {
  let root: string;

  beforeEach(async () => {
    root = await fs.mkdtemp(path.join(os.tmpdir(), 'kb-policy-'));
  });

  afterEach(async () => {
    await fs.rm(root, { recursive: true, force: true });
  });

  async function install(ids: string[], plugins: Record<string, unknown>): Promise<void> {
    const entries: Record<string, ReturnType<typeof createMarketplaceEntry>> = {};
    for (const id of ids) {
      const dir = await makePlugin({ dir: path.join(root, 'node_modules', id.replace('/', '__')), id });
      entries[id] = await lockEntry(root, dir, 'platform');
    }
    await writeLock(root, entries);
    await fs.writeFile(path.join(root, '.kb', 'kb.config.json'), JSON.stringify({ plugins }));
  }

  async function ids(): Promise<{ found: string[]; codes: string[] }> {
    const result = await new DiscoveryManager({ root, scope: 'project' }).discover();
    return { found: result.plugins.map(p => p.id).sort(), codes: result.diagnostics.map(d => d.code) };
  }

  it('applies policy from provenance and ignores package-name prefixes', async () => {
    const thirdParty = await makePlugin({ dir: path.join(root, 'node_modules', 'kb-labs-name'), id: '@kb-labs/name-only' });
    const bad = await makePlugin({ dir: path.join(root, 'node_modules', 'bad'), id: '@x/bad' });
    const shipped = await makePlugin({ dir: path.join(root, 'packages', 'shipped'), id: '@vendor/shipped' });
    await fs.writeFile(path.join(root, 'pnpm-workspace.yaml'), 'packages:\n  - "packages/*"\n');
    await writeLock(root, {
      '@kb-labs/name-only': await lockEntry(root, thirdParty, 'platform'),
      '@x/bad': await lockEntry(root, bad, 'platform'),
    });
    await fs.writeFile(path.join(root, '.kb', 'kb.config.json'), JSON.stringify({ plugins: { block: ['@x/bad', '@vendor/shipped', '@kb-labs/name-only'] } }));

    const { found, codes } = await ids();

    expect(found).toEqual(['@vendor/shipped']);
    expect(codes).toContain('PLUGIN_BLOCKED');
  });

  it('a configured allow list admits only listed and linked third-party packages', async () => {
    await install(['@x/ok', '@x/lnk', '@x/other'], { allow: ['@x/ok'], linked: ['@x/lnk'] });

    const { found, codes } = await ids();

    expect(found).toEqual(['@x/lnk', '@x/ok']);
    expect(codes).toContain('PLUGIN_NOT_ALLOWED');
  });

  it('without an allow list every installed package is admitted', async () => {
    await install(['@x/a', '@x/b'], {});

    expect((await ids()).found).toEqual(['@x/a', '@x/b']);
  });
});

describe('findWorkspaceCandidates', () => {
  let root: string;

  beforeEach(async () => {
    root = await fs.mkdtemp(path.join(os.tmpdir(), 'kb-ws-'));
  });

  afterEach(async () => {
    await fs.rm(root, { recursive: true, force: true });
  });

  it('honours negated patterns and skips unbuilt and manifest-less packages', async () => {
    await makePlugin({ dir: path.join(root, 'plugins', 'a'), id: '@x/a' });
    await makePlugin({ dir: path.join(root, 'plugins', 'skipped'), id: '@x/skipped' });
    await makePlugin({ dir: path.join(root, 'plugins', 'unbuilt'), id: '@x/unbuilt', staticJson: false });
    await fs.mkdir(path.join(root, 'plugins', 'plain'), { recursive: true });
    await fs.writeFile(path.join(root, 'plugins', 'plain', 'package.json'), JSON.stringify({ name: '@x/plain' }));
    await fs.writeFile(path.join(root, 'pnpm-workspace.yaml'), 'packages:\n  - "plugins/*"\n  - "!plugins/skipped"\n');

    const found = await findWorkspaceCandidates(root);

    expect(found.map(c => c.packageName)).toEqual(['@x/a']);
  });

  it('treats a root without pnpm-workspace.yaml as a single package', async () => {
    await makePlugin({ dir: root, id: '@x/solo' });

    const found = await findWorkspaceCandidates(root);

    expect(found).toEqual([{ packageName: '@x/solo', packageRoot: root }]);
  });
});

describe('findWorkspacePackages', () => {
  it('finds configured packages before they are built', async () => {
    const root = await fs.mkdtemp(path.join(os.tmpdir(), 'kb-ws-packages-'));
    try {
      const adapterRoot = path.join(root, 'adapters', 'sqlite');
      await fs.mkdir(adapterRoot, { recursive: true });
      await fs.writeFile(path.join(adapterRoot, 'package.json'), JSON.stringify({ name: '@kb-labs/adapters-sqlite' }));
      await fs.writeFile(path.join(root, 'pnpm-workspace.yaml'), 'packages:\n  - "adapters/*"\n');

      const found = await findWorkspacePackages(root);

      expect(found).toEqual([{ packageName: '@kb-labs/adapters-sqlite', packageRoot: adapterRoot }]);
    } finally {
      await fs.rm(root, { recursive: true, force: true });
    }
  });
});

describe('workspace adapter candidates', () => {
  const moduleFor = (roles: string | string[]) =>
    `export const manifest = ${JSON.stringify({ schema: 'kb.adapter/1', manifestVersion: '1.0.0', id: 'fixture', name: 'Fixture', version: '1.0.0', type: 'core', implements: roles })};\n`;

  it('uses only kb.manifest and classifies undeclared packages as non-adapters', async () => {
    const root = await fs.mkdtemp(path.join(os.tmpdir(), 'kb-adapter-manifest-result-'));
    try {
      const preferred = path.join(root, 'preferred.js');
      const legacy = path.join(root, 'legacy.js');
      await fs.writeFile(path.join(root, 'package.json'), JSON.stringify({
        name: '@fixture/manifest-precedence',
        type: 'module',
        kbLabs: { manifest: './preferred.js' },
        kb: { manifest: './legacy.js' },
      }));
      await fs.writeFile(preferred, moduleFor('IFirst'));
      await fs.writeFile(legacy, moduleFor('ISecond'));
      expect(await loadAdapterManifest({ packageRoot: root })).toMatchObject({ kind: 'adapter', manifest: { implements: 'ISecond' } });

      const multiRoleRoot = path.join(root, 'multi-role');
      await fs.mkdir(multiRoleRoot, { recursive: true });
      await fs.writeFile(path.join(multiRoleRoot, 'package.json'), JSON.stringify({
        name: '@fixture/multi-role',
        type: 'module',
        kb: { manifest: './roles.js' },
      }));
      await fs.writeFile(path.join(multiRoleRoot, 'roles.js'), moduleFor(['IRoleA', 'IRoleB']));
      expect(await loadAdapterManifest({ packageRoot: multiRoleRoot })).toMatchObject({ kind: 'adapter', manifest: { implements: ['IRoleA', 'IRoleB'] } });

      await fs.writeFile(path.join(root, 'package.json'), JSON.stringify({
        name: '@fixture/undeclared',
        type: 'module',
        kbLabs: { manifest: './preferred.js' },
      }));
      await fs.mkdir(path.join(root, 'dist'));
      await fs.writeFile(path.join(root, 'dist/index.js'), 'export const manifest = { implements: "IFallback" };\n');
      expect(await loadAdapterManifest({ packageRoot: root })).toMatchObject({ kind: 'not-adapter', reason: expect.stringContaining('kb.manifest') });

      const brokenRoot = path.join(root, 'broken');
      await fs.mkdir(brokenRoot, { recursive: true });
      await fs.writeFile(path.join(brokenRoot, 'package.json'), JSON.stringify({
        name: '@fixture/broken', type: 'module', kb: { manifest: './dist/manifest.js' },
      }));
      expect(await loadAdapterManifest({ packageRoot: brokenRoot })).toMatchObject({ kind: 'failed' });

      const plainRoot = path.join(root, 'plain');
      await fs.mkdir(path.join(plainRoot, 'dist'), { recursive: true });
      await fs.writeFile(path.join(plainRoot, 'package.json'), JSON.stringify({ name: '@fixture/plain', type: 'module' }));
      await fs.writeFile(path.join(plainRoot, 'dist/index.js'), 'export const manifest = { name: "plain package" };\n');
      expect(await loadAdapterManifest({ packageRoot: plainRoot })).toMatchObject({ kind: 'not-adapter' });

    } finally {
      await fs.rm(root, { recursive: true, force: true });
    }
  });

  it('does not discover the LLM routing wrapper as an ILLM provider', async () => {
    const root = path.resolve(import.meta.dirname, '../../../..');
    const fixtureRoot = await fs.mkdtemp(path.join(os.tmpdir(), 'kb-llm-provider-workspace-'));
    try {
      const routerRoot = path.join(fixtureRoot, 'packages', 'llm-router');
      const providerRoot = path.join(fixtureRoot, 'packages', 'llm-provider');
      await fs.mkdir(path.join(routerRoot, 'dist'), { recursive: true });
      await fs.mkdir(path.join(providerRoot, 'dist'), { recursive: true });
      const routerPackage = JSON.parse(await fs.readFile(path.join(root, 'core/llm-router/package.json'), 'utf8')) as Record<string, unknown>;
      await fs.writeFile(path.join(routerRoot, 'package.json'), JSON.stringify(routerPackage));
      await fs.writeFile(path.join(routerRoot, 'dist', 'manifest.js'), moduleFor('ILLM'));
      await fs.writeFile(path.join(providerRoot, 'package.json'), JSON.stringify({
        name: '@fixture/llm-provider',
        type: 'module',
        kb: { manifest: './dist/manifest.js' },
      }));
      await fs.writeFile(path.join(providerRoot, 'dist', 'manifest.js'), moduleFor('ILLM'));
      await fs.writeFile(path.join(fixtureRoot, 'pnpm-workspace.yaml'), 'packages: ["packages/*"]\n');

      const workspaceCandidates = await findWorkspaceCandidates(fixtureRoot);
      const llmProviders = (await Promise.all(workspaceCandidates.map(async candidate => ({
        packageName: candidate.packageName,
        manifest: await loadAdapterManifest({ packageRoot: candidate.packageRoot }),
      }))))
        .filter(candidate => candidate.manifest.kind === 'adapter' && candidate.manifest.manifest.implements === 'ILLM')
        .map(candidate => candidate.packageName);

      expect(llmProviders).toEqual(['@fixture/llm-provider']);
      expect(llmProviders).not.toContain('@kb-labs/llm-router');
    } finally {
      await fs.rm(fixtureRoot, { recursive: true, force: true });
    }
  });
});
