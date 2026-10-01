import test from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const script = new URL('./prepare-binary-manifest.mjs', import.meta.url);

test('creates Linux and macOS binary entries without unsupported Windows artifacts or author tools', () => {
  const root = mkdtempSync(join(tmpdir(), 'kb-binary-manifest-'));
  const checksums = join(root, 'checksums.txt');
  const output = join(root, 'binary-manifest.json');
  try {
    writeFileSync(checksums, `${'a'.repeat(64)}  kb-create-linux-amd64\n${'b'.repeat(64)}  kb-create-darwin-arm64\n${'c'.repeat(64)}  kb-dev-windows-amd64.exe\n${'d'.repeat(64)}  kb-create-windows-amd64.exe\n${'e'.repeat(64)}  kb-devkit-linux-amd64\nnot-an-asset\n`);
    execFileSync(process.execPath, [script.pathname, '--checksums', checksums, '--repository', 'kb-labs-team/kb-labs', '--release-tag', 'v2.3.4-binaries', '--output', output]);
    assert.deepEqual(JSON.parse(readFileSync(output, 'utf8')), { binaries: [
      { id: 'kb-create', os: 'darwin', arch: 'arm64', filename: 'kb-create-darwin-arm64', sha256: 'b'.repeat(64), url: 'https://github.com/kb-labs-team/kb-labs/releases/download/v2.3.4-binaries/kb-create-darwin-arm64' },
      { id: 'kb-create', os: 'linux', arch: 'amd64', filename: 'kb-create-linux-amd64', sha256: 'a'.repeat(64), url: 'https://github.com/kb-labs-team/kb-labs/releases/download/v2.3.4-binaries/kb-create-linux-amd64' },
    ] });
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
