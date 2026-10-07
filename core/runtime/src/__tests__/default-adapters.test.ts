import { describe, expect, it } from 'vitest';
import { access, mkdir, mkdtemp, readFile, rm, symlink } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { findWorkspacePackages, loadAdapterManifest } from '@kb-labs/core-discovery';
import { validatePlatformAdapterOptions } from '@kb-labs/core-config';
import { resolveRuntimeStatePath } from '@kb-labs/core-project-registry';
import { resolveDiscoveredAdapterDefaults } from '../default-adapters.js';
import type { DiscoveredAdapter } from '../discover-adapters.js';

const workspaceRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../../../');

async function workspaceAdapterManifests(): Promise<Map<string, Record<string, unknown>>> {
  const packages = await findWorkspacePackages(workspaceRoot);
  const manifests = new Map<string, Record<string, unknown>>();

  for (const candidate of packages) {
    if (!(await loadAdapterManifest(candidate.packageRoot))) {continue;}
    const packageJson = JSON.parse(await readFile(path.join(candidate.packageRoot, 'package.json'), 'utf8')) as {
      kb?: { manifest?: string };
      kbLabs?: { manifest?: string };
    };
    const manifestPath = packageJson.kbLabs?.manifest ?? packageJson.kb?.manifest;
    if (!manifestPath) {throw new Error(`${candidate.packageName} has no declared adapter manifest path`);}
    const modulePath = path.resolve(candidate.packageRoot, manifestPath);
    await access(modulePath);
    const loaded = await import(pathToFileURL(modulePath).href) as { manifest?: Record<string, unknown> };
    if (!loaded.manifest) {throw new Error(`${candidate.packageName} does not export its declared adapter manifest`);}
    expect(loaded.manifest.configSchema, `${candidate.packageName} must declare configSchema`).toBeDefined();
    manifests.set(candidate.packageName, loaded.manifest);
  }

  expect(manifests.size).toBeGreaterThan(0);
  return manifests;
}

async function makePlatformRoot(): Promise<string> {
  const platformRoot = await mkdtemp(path.join(tmpdir(), 'kb-adapter-schema-'));
  const scopePath = path.join(platformRoot, 'node_modules', '@kb-labs');
  await mkdir(scopePath, { recursive: true });
  const packages = await findWorkspacePackages(workspaceRoot);
  for (const candidate of packages) {
    if (!(await loadAdapterManifest(candidate.packageRoot))) {continue;}
    await symlink(candidate.packageRoot, path.join(scopePath, candidate.packageName.slice('@kb-labs/'.length)));
  }
  return platformRoot;
}

function discovered(packageName: string, manifest: Record<string, unknown>): DiscoveredAdapter {
  return {
    packageName,
    pkgRoot: `/platform/node_modules/${packageName}`,
    createAdapter: () => ({}),
    module: { manifest },
  };
}

describe('resolveDiscoveredAdapterDefaults', () => {
  // case: S-054.1
  it('validates resolved default options against every real workspace adapter manifest', async () => {
    const manifests = await workspaceAdapterManifests();
    expect(manifests.get('@kb-labs/adapters-log-ringbuffer')?.configSchema).toMatchObject({
      maxSize: { type: 'number', integer: true, minimum: 1 },
      ttl: { type: 'number', integer: true, minimum: 0 },
    });
    expect(manifests.get('@kb-labs/adapters-service-transport-http')?.configSchema).toMatchObject({
      services: { type: 'object', required: true },
      offset: { type: 'number' },
    });
    const discovered = new Map<string, DiscoveredAdapter>();
    for (const [packageName, manifest] of manifests) {
      discovered.set(packageName, {
        packageName,
        pkgRoot: path.join(workspaceRoot, 'node_modules', packageName),
        createAdapter: () => ({}),
        module: { manifest },
      });
    }

    const defaults = resolveDiscoveredAdapterDefaults({ adapters: {}, adapterOptions: {} }, discovered, workspaceRoot);
    expect(Object.keys(defaults.adapterOptions).sort()).toEqual(Object.keys(defaults.adapters).sort());
    const platformRoot = await makePlatformRoot();
    try {
      const platformConfig: Record<string, unknown> = {
        adapters: defaults.adapters,
        adapterOptions: defaults.adapterOptions,
      };
      const issues = await validatePlatformAdapterOptions(platformRoot, platformConfig);
      expect(issues).toEqual([]);
    } finally {
      await rm(platformRoot, { recursive: true, force: true });
    }
  });

  it('uses installed manifest defaults, local data paths, and the SQLite KV entry point', () => {
    const sqlite = discovered('@kb-labs/adapters-sqlite', {
      defaultFor: ['documentDatabase', 'kvStore'],
      implements: ['IDocumentDatabase', 'IKVStore'],
    });
    const storage = discovered('@kb-labs/adapters-fs', {
      defaultFor: ['storage'], implements: 'IStorage',
    });
    const resolved = resolveDiscoveredAdapterDefaults({
      adapters: {}, adapterOptions: { storage: { baseDir: 'user/data' } },
    }, new Map([
      [sqlite.packageName, sqlite],
      [storage.packageName, storage],
    ]), '/project');

    expect(resolved.adapters).toEqual({
      documentDatabase: '@kb-labs/adapters-sqlite',
      kvStore: '@kb-labs/adapters-sqlite/kv',
      storage: '@kb-labs/adapters-fs',
    });
    expect(resolved.adapterOptions).toEqual({
      storage: { baseDir: 'user/data' },
      documentDatabase: { filename: resolveRuntimeStatePath('/project', ['database', 'kb.sqlite']) },
      kvStore: { filename: resolveRuntimeStatePath('/project', ['database', 'kb.sqlite']) },
    });
  });

  it('defaults every persistent built-in adapter path under the machine project state directory', () => {
    const previousHome = process.env.KB_HOME;
    process.env.KB_HOME = '/machine/kb';
    try {
      const sqlite = discovered('@kb-labs/adapters-sqlite', {
        defaultFor: ['documentDatabase', 'kvStore'],
        implements: ['IDocumentDatabase', 'IKVStore'],
      });
      const storage = discovered('@kb-labs/adapters-fs', {
        defaultFor: ['storage'], implements: 'IStorage',
      });
      const analytics = discovered('@kb-labs/adapters-analytics-sqlite', {
        defaultFor: ['analytics'], implements: 'IAnalytics',
      });
      const resolved = resolveDiscoveredAdapterDefaults({ adapters: {}, adapterOptions: {} }, new Map([
        [sqlite.packageName, sqlite], [storage.packageName, storage], [analytics.packageName, analytics],
      ]), '/project');

      expect(resolved.adapterOptions).toEqual({
        storage: { baseDir: resolveRuntimeStatePath('/project', ['storage']) },
        documentDatabase: { filename: resolveRuntimeStatePath('/project', ['database', 'kb.sqlite']) },
        kvStore: { filename: resolveRuntimeStatePath('/project', ['database', 'kb.sqlite']) },
        analytics: {
          filename: resolveRuntimeStatePath('/project', ['analytics', 'analytics.sqlite']),
          retentionDays: 90,
        },
      });
    } finally {
      if (previousHome === undefined) delete process.env.KB_HOME;
      else process.env.KB_HOME = previousHome;
    }
  });

  it('keeps an explicit provider binding ahead of the discovered default', () => {
    const fallback = discovered('@kb-labs/adapters-analytics-sqlite', {
      defaultFor: ['analytics'], implements: 'IAnalytics',
    });
    const resolved = resolveDiscoveredAdapterDefaults({
      adapters: { analytics: '@kb-labs/adapters-analytics-custom' }, adapterOptions: {},
    }, new Map([[fallback.packageName, fallback]]), '/project');

    expect(resolved.adapters.analytics).toBe('@kb-labs/adapters-analytics-custom');
  });

  it('does not implicitly select a provider whose manifest requires configuration', () => {
    const configuredProvider = discovered('@kb-labs/adapters-service-transport-http', {
      defaultFor: ['serviceTransport'],
      implements: 'IServiceTransport',
      configSchema: { services: { type: 'object', required: true } },
    });
    const resolved = resolveDiscoveredAdapterDefaults({ adapters: {}, adapterOptions: {} },
      new Map([[configuredProvider.packageName, configuredProvider]]), '/project');

    expect(resolved.adapters).not.toHaveProperty('serviceTransport');
    expect(resolved.adapterOptions).not.toHaveProperty('serviceTransport');
  });

  it('allows a required secret credential with an environment source as an implicit default', () => {
    const openai = discovered('@kb-labs/adapters-openai', {
      defaultFor: ['llm'],
      implements: 'ILLM',
      configSchema: {
        apiKey: { type: 'string', required: true, secret: true, env: 'OPENAI_API_KEY' },
        model: { type: 'string', default: 'gpt-4o' },
      },
    });
    const resolved = resolveDiscoveredAdapterDefaults({ adapters: {}, adapterOptions: {} },
      new Map([[openai.packageName, openai]]), '/project');

    expect(resolved.adapters.llm).toBe('@kb-labs/adapters-openai');
  });
});
