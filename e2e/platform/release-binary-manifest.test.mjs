import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../..', import.meta.url));
const binaryManifestTool = join(root, 'tools/kb-create/scripts/prepare-binary-manifest.mjs');
const releaseIndexTool = join(root, 'tools/kb-create/scripts/prepare-release-index.mjs');

test('candidate binary manifest from release tooling passes release-index preparation', () => {
  const fixture = mkdtempSync(join(tmpdir(), 'kb-journey-release-manifest-'));
  try {
    const artifacts = join(fixture, 'artifacts');
    mkdirSync(artifacts);
    const amd64 = join(fixture, 'kb-create-linux-amd64');
    const arm64 = join(fixture, 'kb-create-linux-arm64');
    writeFileSync(amd64, 'fixture amd64 binary');
    writeFileSync(arm64, 'fixture arm64 binary');
    const checksums = join(fixture, 'checksums.txt');
    writeFileSync(checksums, [amd64, arm64].map(path => {
      const filename = path.split('/').at(-1);
      const digest = createHash('sha256').update(readFileSync(path)).digest('hex');
      return `${digest}  ${filename}`;
    }).join('\n') + '\n');

    const binaryManifest = join(fixture, 'binary-manifest.json');
    execFileSync(process.execPath, [binaryManifestTool,
      '--checksums', checksums,
      '--repository', 'kb-labs-team/kb-labs',
      '--release-tag', 'journey-candidate',
      '--version', '1.0.0',
      '--asset-base-url', 'http://origin:8080/journey-candidate',
      '--output', binaryManifest,
    ]);

    const packageDir = join(fixture, 'package');
    mkdirSync(packageDir);
    writeFileSync(join(packageDir, 'package.json'), JSON.stringify({
      name: '@kb-labs/core-runtime',
      version: '1.0.0',
      bin: { 'kb-host': './host.js' },
      peerDependencies: { '@kb-labs/core-runtime': '^1.0.0' },
    }));
    writeFileSync(join(packageDir, 'host.js'), '#!/usr/bin/env node\n');
    const tarball = 'core-runtime-1.0.0.tgz';
    execFileSync('tar', ['-czf', join(artifacts, tarball), '-C', fixture, 'package']);
    const artifactHash = createHash('sha256').update(readFileSync(join(artifacts, tarball))).digest('hex');
    writeFileSync(join(artifacts, 'manifest.json'), JSON.stringify([{
      name: '@kb-labs/core-runtime', version: '1.0.0', tarball, sha256: artifactHash,
    }]));

    const releaseIndex = join(fixture, 'release-index.json');
    const sealer = join(fixture, 'fixture-sealer.mjs');
    writeFileSync(sealer, '#!/usr/bin/env node\nimport { copyFileSync } from "node:fs";\nconst args = process.argv.slice(2);\ncopyFileSync(args[args.indexOf("--input") + 1], args[args.indexOf("--output") + 1]);\n');
    chmodSync(sealer, 0o755);
    execFileSync(process.execPath, [releaseIndexTool,
      '--flow', 'platform',
      '--artifacts-dir', artifacts,
      '--binary-manifest', binaryManifest,
      '--platform-package', '@kb-labs/core-runtime',
      '--sdk-package', '@kb-labs/core-runtime',
      '--host-package', '@kb-labs/core-runtime',
      '--platform-member-packages', '@kb-labs/core-runtime',
      '--registry', 'http://registry.fixture',
      '--output', releaseIndex,
      '--sealer-bin', sealer,
    ]);

    const index = JSON.parse(readFileSync(releaseIndex, 'utf8'));
    assert.equal(index.platforms[0].version, '1.0.0');
    assert.deepEqual(index.platforms[0].binaries.map(({ id, version, os, arch, url }) =>
      ({ id, version, os, arch, url })), [
      { id: 'kb-create', version: '1.0.0', os: 'linux', arch: 'amd64', url: 'http://origin:8080/journey-candidate/kb-create-linux-amd64' },
      { id: 'kb-create', version: '1.0.0', os: 'linux', arch: 'arm64', url: 'http://origin:8080/journey-candidate/kb-create-linux-arm64' },
    ]);
    assert.deepEqual(index.platforms[0].binaries.map(({ sha256 }) => sha256), [
      createHash('sha256').update('fixture amd64 binary').digest('hex'),
      createHash('sha256').update('fixture arm64 binary').digest('hex'),
    ]);
  } finally {
    rmSync(fixture, { recursive: true, force: true });
  }
});
