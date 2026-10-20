import * as fs from 'node:fs/promises';
import * as os from 'node:os';
import * as path from 'node:path';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { readStaticManifest } from '../manifest.js';
import { sampleManifest } from './fixtures.js';

describe('readStaticManifest', () => {
  let dir: string;

  beforeEach(async () => {
    dir = await fs.mkdtemp(path.join(os.tmpdir(), 'kb-extension-store-manifest-'));
  });

  afterEach(async () => {
    await fs.rm(dir, { recursive: true, force: true });
  });

  it('returns undefined when there is no package.json and no dist/manifest.json', async () => {
    await expect(readStaticManifest(dir)).resolves.toBeUndefined();
  });

  it('reads the conventional dist/manifest.json', async () => {
    const manifest = sampleManifest('acme-deploy', '1.0.0');
    await fs.writeFile(path.join(dir, 'package.json'), JSON.stringify({ name: 'acme-deploy' }));
    await fs.mkdir(path.join(dir, 'dist'), { recursive: true });
    await fs.writeFile(path.join(dir, 'dist', 'manifest.json'), JSON.stringify(manifest));

    await expect(readStaticManifest(dir)).resolves.toEqual(manifest);
  });

  it('follows an explicit kb.manifest field to a static JSON file', async () => {
    const manifest = sampleManifest('acme-deploy', '1.0.0');
    await fs.writeFile(
      path.join(dir, 'package.json'),
      JSON.stringify({ name: 'acme-deploy', kb: { manifest: './custom/manifest.json' } }),
    );
    await fs.mkdir(path.join(dir, 'custom'), { recursive: true });
    await fs.writeFile(path.join(dir, 'custom', 'manifest.json'), JSON.stringify(manifest));

    await expect(readStaticManifest(dir)).resolves.toEqual(manifest);
  });

  it('ignores the non-canonical kbLabs.manifest field', async () => {
    const manifest = sampleManifest('acme-deploy', '1.0.0');
    await fs.writeFile(
      path.join(dir, 'package.json'),
      JSON.stringify({ name: 'acme-deploy', kbLabs: { manifest: './custom/manifest.json' } }),
    );
    await fs.mkdir(path.join(dir, 'custom'), { recursive: true });
    await fs.writeFile(path.join(dir, 'custom', 'manifest.json'), JSON.stringify(manifest));

    await expect(readStaticManifest(dir)).resolves.toBeUndefined();
  });

  it('never imports plugin JS: a kb.manifest field pointing at a .js file is not executed', async () => {
    await fs.writeFile(
      path.join(dir, 'package.json'),
      JSON.stringify({ name: 'acme-deploy', kb: { manifest: './dist/manifest.js' } }),
    );
    await fs.mkdir(path.join(dir, 'dist'), { recursive: true });
    // A .js manifest with no static .json sibling: must not be imported, and must not throw.
    await fs.writeFile(path.join(dir, 'dist', 'manifest.js'), 'throw new Error("must never run");');

    await expect(readStaticManifest(dir)).resolves.toBeUndefined();
  });

  it('ignores a kb.manifest field that escapes the package root', async () => {
    await fs.writeFile(
      path.join(dir, 'package.json'),
      JSON.stringify({ name: 'acme-deploy', kb: { manifest: '../../etc/manifest.json' } }),
    );
    await expect(readStaticManifest(dir)).resolves.toBeUndefined();
  });

  it('returns undefined for a manifest of another schema', async () => {
    await fs.writeFile(path.join(dir, 'package.json'), JSON.stringify({ name: 'acme-deploy' }));
    await fs.mkdir(path.join(dir, 'dist'), { recursive: true });
    await fs.writeFile(path.join(dir, 'dist', 'manifest.json'), JSON.stringify({ schema: 'kb.service/1' }));

    await expect(readStaticManifest(dir)).resolves.toBeUndefined();
  });
});
