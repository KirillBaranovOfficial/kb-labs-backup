import { describe, expect, it } from 'vitest';
import { resolveRuntimeStatePath } from '@kb-labs/core-project-registry';
import { resolveDiscoveredAdapterDefaults } from '../default-adapters.js';
import type { DiscoveredAdapter } from '../discover-adapters.js';

function discovered(packageName: string, manifest: Record<string, unknown>): DiscoveredAdapter {
  return {
    packageName,
    pkgRoot: `/platform/node_modules/${packageName}`,
    createAdapter: () => ({}),
    module: { manifest },
  };
}

describe('resolveDiscoveredAdapterDefaults', () => {
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
        analytics: { filename: resolveRuntimeStatePath('/project', ['analytics', 'analytics.sqlite']) },
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
