import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { runStage } from './remote-verify-stage.mjs';

const profile = JSON.parse(readFileSync(new URL('./verification-profile.json', import.meta.url), 'utf8'));

// case: S-262.7
test('workspace package shard uses kb-devkit and records a stage result with elapsed time', async () => {
  let now = 1_000;
  let invocation;
  const result = await runStage({
    stage: profile.packageTestStage,
    packageNames: ['@kb-labs/core', '@kb-labs/cli'],
    shard: 3,
    runCommand: async (command, args) => {
      invocation = [command, args];
      now += 250;
      return { status: 0, stdout: 'passed', stderr: '' };
    },
    now: () => now,
  });

  assert.deepEqual(invocation, ['./tools/kb-devkit/kb-devkit', ['run', 'test', '--packages', '@kb-labs/core,@kb-labs/cli', '--concurrency', '1']]);
  assert.equal(result.name, `${profile.packageTestStage}:shard-3`);
  assert.deepEqual(result.packages, ['@kb-labs/core', '@kb-labs/cli']);
  assert.equal(result.status, 'pass');
  assert.equal(result.durationMs, 250);
});

// case: S-262.7
test('journey unit shard records its failure instead of losing it before artifact upload', async () => {
  const result = await runStage({
    stage: profile.buildDependentTestStages.candidate[0],
    runCommand: async () => ({ status: 1, stdout: '', stderr: 'test failure' }),
    now: (() => { let value = 0; return () => value += 10; })(),
  });

  assert.equal(result.status, 'fail');
  assert.match(result.logTail, /test failure/);
  assert.equal(result.durationMs, 10);
});
