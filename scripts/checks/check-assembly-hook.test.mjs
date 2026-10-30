import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import test from 'node:test';
import { CHECK_RESULT_FILE_ENV } from './lib/check-result.mjs';

const root = path.resolve(import.meta.dirname, '../..');

test('check-assembly-hook writes an empty envelope when a non-anchor package skips', () => {
  const directory = mkdtempSync(path.join(os.tmpdir(), 'kb-check-assembly-hook-'));
  try {
    const resultFile = path.join(directory, 'result.json');
    const result = spawnSync(process.execPath, ['scripts/checks/check-assembly-hook.mjs'], {
      cwd: root,
      encoding: 'utf8',
      env: {
        ...process.env,
        KB_DEVKIT_MODE: 'check',
        KB_DEVKIT_PACKAGE_NAME: '@fixture/non-anchor',
        [CHECK_RESULT_FILE_ENV]: resultFile,
      },
    });

    assert.equal(result.status, 0, result.stderr);
    assert.deepEqual(JSON.parse(readFileSync(resultFile, 'utf8')), { issues: [] });
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});
