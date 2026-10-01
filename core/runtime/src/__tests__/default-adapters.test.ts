import { describe, expect, it } from 'vitest';
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
      documentDatabase: { filename: '.kb/database/kb.sqlite' },
      kvStore: { filename: '.kb/database/kb.sqlite' },
    });
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
});
