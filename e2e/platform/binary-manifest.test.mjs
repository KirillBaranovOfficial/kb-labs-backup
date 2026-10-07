import assert from 'node:assert/strict';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { buildBinaryManifest } from './binary-manifest.mjs';

test('builds sealed release entries for every supported kb-create architecture', () => {
  const root = mkdtempSync(join(tmpdir(), 'kb-create-binary-manifest-'));
  try {
    writeFileSync(join(root, 'kb-create-linux-amd64'), 'amd64 binary');
    writeFileSync(join(root, 'kb-create-linux-arm64'), 'arm64 binary');

    assert.deepEqual(buildBinaryManifest(root, 'http://origin:8080/journey-candidate'), {
      binaries: [
        {
          id: 'kb-create', os: 'linux', arch: 'amd64',
          url: 'http://origin:8080/journey-candidate/kb-create-linux-amd64',
          filename: 'kb-create-linux-amd64',
          sha256: 'acda34fda4038346efbcb7e0c04167719e65fa5fe8ac1da1ead9323842922bee',
        },
        {
          id: 'kb-create', os: 'linux', arch: 'arm64',
          url: 'http://origin:8080/journey-candidate/kb-create-linux-arm64',
          filename: 'kb-create-linux-arm64',
          sha256: '69c8d344d2c80dfef71eb48359dd49d284f333515ad4d9c838dad09101f9c512',
        },
      ],
    });
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('fails when a supported client binary is absent', () => {
  const root = mkdtempSync(join(tmpdir(), 'kb-create-binary-manifest-'));
  try {
    writeFileSync(join(root, 'kb-create-linux-amd64'), 'amd64 binary');
    assert.throws(() => buildBinaryManifest(root, 'http://origin:8080/journey-candidate'),
      /kb-create-linux-arm64 is missing/);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
