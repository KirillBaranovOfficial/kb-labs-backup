import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

const script = fileURLToPath(new URL('./new-tests.mjs', import.meta.url));

test('new-tests emits the devkit external-check contract', () => {
  const env = { ...process.env };
  delete env.KB_DEVKIT_BASE_REF;
  const result = spawnSync(process.execPath, [script], {
    cwd: process.cwd(),
    encoding: 'utf8',
    env,
  });

  assert.equal(result.status, 0, result.stderr);
  assert.deepEqual(JSON.parse(result.stdout), { issues: [] });
});
