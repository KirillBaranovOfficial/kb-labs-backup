import { describe, expect, it } from 'vitest';
import type { IExtensionStore, StoreEntry } from '@kb-labs/core-extension-store';
import { DiscoveryManager } from '../discovery-manager.js';

function storeWithEntries(entries: StoreEntry[]): IExtensionStore {
  return {
    root: '/mock/store',
    casDir: '/mock/store/cas',
    entriesDir: '/mock/store/entries',
    lookup: async () => undefined,
    resolve: async () => { throw new Error('resolve must not run in an absent-route lookup'); },
    listEntries: async () => entries,
  };
}

describe('stored plugin REST prefix lookup', () => {
  it('resolves the exact package name from its stored manifest after uninstall', async () => {
    const entry = {
      schema: 'kb.store-entry/1',
      name: '@third-party/custom-rest-plugin',
      version: '4.2.0',
      kind: 'plugin',
      integrity: 'sha512-test',
      installedAt: '2026-10-01T00:00:00.000Z',
      manifest: {
        schema: 'kb.plugin/3',
        id: '@third-party/custom-rest-plugin',
        version: '4.2.0',
        rest: { basePath: '/v1/plugins/custom', routes: [{ method: 'GET', path: '/status', handler: './status.js#default' }] },
      },
    } as StoreEntry;
    const manager = new DiscoveryManager({ workspace: false, extensionStore: storeWithEntries([entry]) });

    await expect(manager.findStoredPluginPackageForRestPrefix('/api/v1/plugins/custom', '/api/v1'))
      .resolves.toBe('@third-party/custom-rest-plugin');
    await expect(manager.findStoredPluginPackageForRestPrefix('/api/v1/plugins/unknown', '/api/v1'))
      .resolves.toBeUndefined();
  });
});
