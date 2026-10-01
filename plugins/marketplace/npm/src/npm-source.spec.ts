import { afterEach, describe, expect, it, vi } from 'vitest';
import * as fs from 'node:fs/promises';
import * as os from 'node:os';
import * as path from 'node:path';
import { assertSealedPlatformOverrides, NpmPackageSource, PackageMetadataError, PlatformArtifactPinsError } from './npm-source.js';

describe('NpmPackageSource', () => {
  afterEach(() => vi.unstubAllGlobals());

  function packument(packageId: string, kb: { manifest?: string; adapter?: unknown }, version = '1.0.0') {
    return new Response(JSON.stringify({
      name: packageId,
      'dist-tags': { latest: version },
      versions: { [version]: { name: packageId, version, kb } },
    }), { status: 200 });
  }

  it('resolves plugin kind from exact-version package manifest metadata', async () => {
    const fetch = vi.fn(async () => packument('@kb-labs/custom', { manifest: './dist/manifest.js' }));
    vi.stubGlobal('fetch', fetch);
    const source = new NpmPackageSource({ registryUrl: 'https://registry.test' });

    const result = await source.resolve('@kb-labs/custom@1.0.0');

    expect(result).toMatchObject({ id: '@kb-labs/custom', version: '1.0.0', primaryKind: 'plugin' });
    expect(fetch).toHaveBeenCalledWith('https://registry.test/@kb-labs%2Fcustom');
  });

  it('resolves adapter kind from exact-version package manifest metadata', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => packument('@kb-labs/adapters-file', { adapter: { manifest: './dist/manifest.js' } })));
    const result = await new NpmPackageSource({ registryUrl: 'https://registry.test' })
      .resolve('@kb-labs/adapters-file');
    expect(result).toMatchObject({ id: '@kb-labs/adapters-file', version: '1.0.0', primaryKind: 'adapter' });
  });

  it('fails with a typed error when exact-version metadata cannot classify the package', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => packument('@kb-labs/unknown-entry', {})));
    await expect(new NpmPackageSource({ registryUrl: 'https://registry.test' }).resolve('@kb-labs/unknown-entry@1.0.0'))
      .rejects.toMatchObject({ name: 'PackageMetadataError', code: 'UNKNOWN_ENTITY_KIND' });
  });

  it('validates every sealed artifact from the receipt when root dependencies contain no file specs', async () => {
    const root = await fs.mkdtemp(path.join(os.tmpdir(), 'kb-marketplace-pnpm-overrides-'));
    try {
      await fs.mkdir(path.join(root, '.kb', 'v2'), { recursive: true });
      await fs.writeFile(path.join(root, '.kb', 'v2', 'receipt.json'), JSON.stringify({
        id: 'sealed-platform',
        plan: { artifacts: [
          { id: 'workflow', package: '@kb-labs/workflow-daemon', sha256: 'abc123', tarball: 'registry/workflow.tgz' },
          { id: 'gateway', package: '@kb-labs/gateway-app', sha256: 'def456', tarball: 'registry/gateway.tgz' },
          { id: 'binary-tool', kind: 'binary', package: 'tool', sha256: 'xyz', tarball: 'registry/tool.tgz' },
        ] },
      }));
      const packageFile = path.join(root, 'package.json');
      await fs.writeFile(packageFile, JSON.stringify({
        name: 'kb-platform',
        dependencies: {},
      }));
      const workspaceFile = path.join(root, 'pnpm-workspace.yaml');
      const original = 'allowBuilds:\n  esbuild: true\noverrides:\n  lodash: 4.17.21\n';
      await fs.writeFile(workspaceFile, original);

      const error = await assertSealedPlatformOverrides(root, '@kb-labs/adapters-analytics-file', '2.120.1')
        .catch(value => value as PlatformArtifactPinsError);

      expect(error).toMatchObject({
        name: 'PlatformArtifactPinsError',
        code: 'MARKETPLACE_PLATFORM_ARTIFACT_PINS_MISSING',
        message: "The installed platform is missing kb-create's sealed artifact overrides. No package was installed.",
        hint: `Run "kb-create update --platform-root ${JSON.stringify(root)}" and retry "kb marketplace install @kb-labs/adapters-analytics-file@2.120.1 --scope platform".`,
      });
      expect(await fs.readFile(workspaceFile, 'utf8')).toBe(original);
      expect(JSON.parse(await fs.readFile(packageFile, 'utf8')).dependencies).toEqual({});
      await fs.writeFile(workspaceFile, [
        'overrides:',
        '  "@kb-labs/workflow-daemon": file:.kb/v2/cache/packages/abc123.tgz',
        '  "@kb-labs/gateway-app": file:.kb/v2/cache/packages/def456.tgz',
        '',
      ].join('\n'));
      await expect(assertSealedPlatformOverrides(root, '@kb-labs/adapters-analytics-file', '2.120.1'))
        .resolves.toBeUndefined();
    } finally {
      await fs.rm(root, { recursive: true, force: true });
    }
  });

  it('does not pass pin validation when the installed receipt has no sealed artifact set', async () => {
    const root = await fs.mkdtemp(path.join(os.tmpdir(), 'kb-marketplace-empty-receipt-'));
    try {
      await fs.mkdir(path.join(root, '.kb', 'v2'), { recursive: true });
      await fs.writeFile(path.join(root, '.kb', 'v2', 'receipt.json'), JSON.stringify({ plan: { artifacts: [] } }));
      await fs.writeFile(path.join(root, 'package.json'), JSON.stringify({ name: 'kb-platform', dependencies: {} }));
      await fs.writeFile(path.join(root, 'pnpm-workspace.yaml'), 'overrides:\n  lodash: 4.17.21\n');

      await expect(assertSealedPlatformOverrides(root, '@kb-labs/adapters-analytics-file', '2.120.1'))
        .rejects.toMatchObject({ code: 'MARKETPLACE_PLATFORM_ARTIFACT_PINS_MISSING' });
    } finally {
      await fs.rm(root, { recursive: true, force: true });
    }
  });

  it('uses a typed missing-package error for registry 404', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('', { status: 404 })));
    const error = await new NpmPackageSource({ registryUrl: 'https://registry.test' })
      .resolve('@kb-labs/missing@1.0.0').catch(value => value as PackageMetadataError);
    expect(error).toMatchObject({ code: 'PACKAGE_NOT_FOUND', packageId: '@kb-labs/missing' });
  });
});
