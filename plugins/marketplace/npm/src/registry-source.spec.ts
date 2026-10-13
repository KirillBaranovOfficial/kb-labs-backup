import { afterEach, describe, expect, it, vi } from 'vitest';
import * as fs from 'node:fs/promises';
import * as os from 'node:os';
import * as path from 'node:path';
import { RegistryPackageSource } from './registry-source.js';

describe('RegistryPackageSource', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('preserves the registry entity kind when resolving a package', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({
      primaryKind: 'plugin',
      versions: [{ version: '1.0.0', integrity: 'sha512-fixture' }],
    }), { status: 200 })));

    const resolved = await new RegistryPackageSource({ registryUrl: 'https://registry.test' })
      .resolve('kb:author/custom-plugin@1.0.0');

    expect(resolved).toMatchObject({
      id: 'author/custom-plugin',
      version: '1.0.0',
      primaryKind: 'plugin',
    });
  });

  // case: S-220.11
  it('restores an existing package when registry archive extraction fails', async () => {
    const root = await fs.mkdtemp(path.join(os.tmpdir(), 'kb-registry-rollback-'));
    const packageRoot = path.join(root, 'node_modules', 'author', 'broken-adapter');
    const originalManifest = JSON.stringify({ name: 'author/broken-adapter', version: '0.9.0' });
    try {
      await fs.mkdir(packageRoot, { recursive: true });
      await fs.writeFile(path.join(packageRoot, 'package.json'), originalManifest);
      vi.stubGlobal('fetch', vi.fn(async () => new Response('not a tar archive', { status: 200 })));

      const source = new RegistryPackageSource({ registryUrl: 'https://registry.test' });
      await expect(source.install({
        id: 'author/broken-adapter',
        version: '1.0.0',
        integrity: '',
        source: 'marketplace',
        primaryKind: 'adapter',
        downloadUrl: 'https://registry.test/broken-adapter.tgz',
      }, root)).rejects.toThrow();

      expect(await fs.readFile(path.join(packageRoot, 'package.json'), 'utf8')).toBe(originalManifest);
      expect(await fs.readdir(packageRoot)).toEqual(['package.json']);
    } finally {
      await fs.rm(root, { recursive: true, force: true });
    }
  });
});
