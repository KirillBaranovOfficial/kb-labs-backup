import assert from 'node:assert/strict';
import test from 'node:test';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { runExternalCheck } from './lib/run-external-check.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');

test('check-assembly-hook writes an empty envelope when a non-anchor package skips', () => {
  const result = runExternalCheck('scripts/checks/check-assembly-hook.mjs', [], {
    cwd: root,
    env: { KB_DEVKIT_MODE: 'check', KB_DEVKIT_PACKAGE_NAME: '@fixture/non-anchor' },
  });
  assert.equal(result.exitCode, 0, result.stderr);
  assert.deepEqual(result.result, { issues: [] });
});
