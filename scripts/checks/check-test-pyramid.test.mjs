import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const script = fileURLToPath(new URL('./check-test-pyramid.mjs', import.meta.url));

test('test-pyramid check drains its structured stdout before successful exit', () => {
  const root = mkdtempSync(path.join(os.tmpdir(), 'test-pyramid-output-'));
  try {
    const commands = path.join(root, 'src/commands');
    mkdirSync(commands, { recursive: true });
    for (let index = 0; index < 900; index += 1) {
      writeFileSync(path.join(commands, `command-${index}.ts`), 'export {};');
    }
    const result = spawnSync(process.execPath, [script], {
      cwd: root,
      env: { ...process.env, KB_DEVKIT_CHECK_NAME: 'test-pyramid' },
      input: JSON.stringify({ package: { name: 'fixture', scripts: { 'test:cli': 'vitest run' } } }),
      encoding: 'utf8',
    });
    assert.equal(result.status, 0, result.stderr);
    const output = JSON.parse(result.stdout);
    assert.equal(output.issues.length, 901);
    assert.deepEqual(output.issues[0], {
      check: 'test-pyramid',
      severity: 'warning',
      message: 'Command "command-0.ts" has no handler test. Add src/__tests__/cli/command-0.cli.test.ts.',
      file: 'src/__tests__/cli/command-0.cli.test.ts',
      fix: 'touch src/__tests__/cli/command-0.cli.test.ts',
    });
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
