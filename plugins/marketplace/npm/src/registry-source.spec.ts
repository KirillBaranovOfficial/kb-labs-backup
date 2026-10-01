import { afterEach, describe, expect, it, vi } from 'vitest';
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
});
