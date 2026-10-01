import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { promises as fs } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { configuredAdapterLoadError, loadDiscoveredAdapterModule } from '../adapter-module-loader.js';
import { AdapterUnavailableError } from '@kb-labs/core-platform';
import type { DiscoveredAdapter } from '../discover-adapters.js';

describe('loadDiscoveredAdapterModule', () => {
  it('creates a typed, actionable error for a bound adapter package that cannot load', () => {
    const error = configuredAdapterLoadError('llm', '@kb-labs/adapters-openai');

    expect(error).toBeInstanceOf(AdapterUnavailableError);
    expect(error.slot).toBe('llm');
    expect(error.reason).toBe('load-failed');
    expect(error.message).toContain('package "@kb-labs/adapters-openai"');
    expect(error.message).toContain('kb marketplace install @kb-labs/adapters-openai --scope platform');
  });

  let root: string;
  let adapter: DiscoveredAdapter;

  beforeEach(async () => {
    root = await fs.mkdtemp(path.join(os.tmpdir(), 'adapter-module-loader-'));
    const dist = path.join(root, 'dist');
    await fs.mkdir(dist, { recursive: true });
    await fs.writeFile(path.join(dist, 'index.js'), `export const manifest = { implements: 'IDocumentDatabase' }; export function createAdapter() { return { kind: 'document' }; }`);
    await fs.writeFile(path.join(dist, 'kv.js'), `export const manifest = { implements: 'IKVStore' }; export function createAdapter() { return { get() { return 'kv'; } }; }`);
    adapter = { packageName: '@test/sqlite', pkgRoot: root, createAdapter: () => ({}), module: {} };
  });

  afterEach(async () => {
    await fs.rm(root, { recursive: true, force: true });
  });

  it('loads the requested KV subpath factory instead of the package root', async () => {
    const module = await loadDiscoveredAdapterModule(adapter, '@test/sqlite/kv');
    const store = module.createAdapter({}, {}) as { get(): string };

    expect(module.manifest.implements).toBe('IKVStore');
    expect(store.get()).toBe('kv');
  });

  it('fails when a requested subpath is missing instead of loading the root adapter', async () => {
    await expect(loadDiscoveredAdapterModule(adapter, '@test/sqlite/kv-missing'))
      .rejects.toThrow('kv-missing.js');
  });
});
