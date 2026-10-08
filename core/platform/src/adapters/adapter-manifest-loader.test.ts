import { afterEach, describe, expect, it } from 'vitest';
import * as fs from 'node:fs/promises';
import * as os from 'node:os';
import * as path from 'node:path';
import { declaredManifestPath, loadAdapterManifest } from './adapter-manifest-loader.js';

describe('declaredManifestPath', () => {
  it('reads only the canonical kb.manifest field', () => {
    expect(declaredManifestPath({ kb: { manifest: './dist/manifest.js' } })).toBe('./dist/manifest.js');
    expect(declaredManifestPath({ kbLabs: { manifest: './dist/legacy.js' } })).toBeUndefined();
    expect(declaredManifestPath({})).toBeUndefined();
  });
});

describe('loadAdapterManifest', () => {
  let tempRoot: string | undefined;

  afterEach(async () => {
    if (tempRoot) {await fs.rm(tempRoot, { recursive: true, force: true });}
    tempRoot = undefined;
  });

  it('loads a typed manifest from the sole kb.manifest declaration for an installed package', async () => {
    tempRoot = await fs.mkdtemp(path.join(os.tmpdir(), 'kb-adapter-manifest-loader-'));
    const packageRoot = path.join(tempRoot, 'node_modules', '@fixture', 'cache');
    await fs.mkdir(packageRoot, { recursive: true });
    await fs.writeFile(path.join(packageRoot, 'package.json'), JSON.stringify({
      name: '@fixture/cache', type: 'module', kb: { manifest: './manifest.mjs' },
    }));
    await fs.writeFile(path.join(packageRoot, 'manifest.mjs'), `export const manifest = ${JSON.stringify({
      schema: 'kb.adapter/1', manifestVersion: '1.0.0', id: 'fixture-cache', name: 'Fixture Cache', version: '1.0.0',
      type: 'core', implements: 'ICache', configSchema: { directory: { type: 'string' } },
    })};`);

    const result = await loadAdapterManifest({ spec: '@fixture/cache', platformRoot: tempRoot });
    expect(result.kind).toBe('adapter');
    if (result.kind !== 'adapter') {throw new Error('expected adapter manifest');}
    expect(result.manifest.requires).toBeUndefined();
    expect(result.manifest.configSchema?.directory?.type).toBe('string');
  });

  it('returns not-adapter for packages without kb.manifest', async () => {
    tempRoot = await fs.mkdtemp(path.join(os.tmpdir(), 'kb-adapter-manifest-loader-'));
    const packageRoot = path.join(tempRoot, 'plain');
    await fs.mkdir(packageRoot, { recursive: true });
    await fs.writeFile(path.join(packageRoot, 'package.json'), JSON.stringify({
      name: 'plain', kbLabs: { manifest: './manifest.mjs' },
    }));
    await fs.writeFile(path.join(packageRoot, 'manifest.mjs'), 'export const manifest = {};');

    await expect(loadAdapterManifest({ packageRoot })).resolves.toMatchObject({
      kind: 'not-adapter', reason: expect.stringContaining('kb.manifest'),
    });
  });

  it('returns failed when a declared module does not export a valid adapter manifest', async () => {
    tempRoot = await fs.mkdtemp(path.join(os.tmpdir(), 'kb-adapter-manifest-loader-'));
    const packageRoot = path.join(tempRoot, 'broken');
    await fs.mkdir(packageRoot, { recursive: true });
    await fs.writeFile(path.join(packageRoot, 'package.json'), JSON.stringify({
      name: 'broken', type: 'module', kb: { manifest: './manifest.mjs' },
    }));
    await fs.writeFile(path.join(packageRoot, 'manifest.mjs'), 'export const manifest = { schema: "kb.adapter/1", implements: "ICache" };');

    await expect(loadAdapterManifest({ packageRoot })).resolves.toMatchObject({ kind: 'failed' });
  });

  it.each([
    ['missing', ''],
    ['wrong', 'schema: "kb.adapter/2", '],
  ] as const)('returns failed when a declared adapter manifest has a %s schema', async (_kind, schemaField) => {
    tempRoot = await fs.mkdtemp(path.join(os.tmpdir(), 'kb-adapter-manifest-loader-'));
    const packageRoot = path.join(tempRoot, 'invalid-schema');
    await fs.mkdir(packageRoot, { recursive: true });
    await fs.writeFile(path.join(packageRoot, 'package.json'), JSON.stringify({
      name: 'invalid-schema', type: 'module', kb: { manifest: './manifest.mjs' },
    }));
    await fs.writeFile(path.join(packageRoot, 'manifest.mjs'), `export const manifest = { ${schemaField}implements: "ICache" };`);

    await expect(loadAdapterManifest({ packageRoot })).resolves.toMatchObject({
      kind: 'failed', reason: expect.stringContaining('adapter manifest must declare schema "kb.adapter/1"'),
    });
  });
});
