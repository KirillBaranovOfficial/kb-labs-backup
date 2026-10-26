import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { aggregateVerdict } from './aggregate-remote-verdict.mjs';

const profile = JSON.parse(readFileSync(new URL('./verification-profile.json', import.meta.url), 'utf8'));

// case: S-262.7
test('aggregates every shard with the existing verdict shape and per-stage timing', () => {
  const verdict = aggregateVerdict({
    sha: 'a'.repeat(40),
    build: { name: profile.buildStage, status: 'pass', selectedPackageCount: 41, failures: [], logTail: '', durationMs: 300 },
    matrix: { include: [{ stage: profile.buildDependentTestStages.candidate[0] }, { stage: profile.packageTestStage, shard: 1, packages: ['@kb-labs/core'] }] },
    results: [
      { name: profile.buildDependentTestStages.candidate[0], status: 'pass', selectedPackageCount: 41, failures: [], logTail: '', durationMs: 70 },
      { name: `${profile.packageTestStage}:shard-1`, status: 'fail', selectedPackageCount: 1, failures: ['test failed'], logTail: 'test failed', durationMs: 40 },
    ],
    selectedPackageCount: 41,
  });

  assert.equal(verdict.schemaVersion, 1);
  assert.equal(verdict.status, 'fail');
  assert.deepEqual(verdict.jobs.map(({ name }) => name), [profile.buildStage, profile.buildDependentTestStages.candidate[0], `${profile.packageTestStage}:shard-1`]);
  assert.deepEqual(verdict.jobs.map(({ durationMs }) => durationMs), [300, 70, 40]);
  assert.deepEqual(verdict.jobs.map(({ selectedPackageCount }) => selectedPackageCount), [41, 41, 1]);
});

// case: S-262.7
test('marks matrix shards cancelled by stream fail-fast as skipped, never as a passing verdict', () => {
  const verdict = aggregateVerdict({
    sha: 'b'.repeat(40),
    build: { name: profile.buildStage, status: 'pass', selectedPackageCount: 41, failures: [], logTail: '', durationMs: 300 },
    matrix: { include: [{ stage: profile.buildDependentTestStages.candidate[0] }, { stage: profile.independentTestStages.candidate[1] }] },
    results: [{ name: profile.buildDependentTestStages.candidate[0], status: 'fail', selectedPackageCount: 41, failures: ['failed'], logTail: 'failed', durationMs: 70 }],
    selectedPackageCount: 41,
  });

  assert.equal(verdict.status, 'fail');
  assert.equal(verdict.jobs[2].name, 'kb-create-go-suite');
  assert.equal(verdict.jobs[2].status, 'skipped');
  assert.equal(verdict.jobs[2].selectedPackageCount, 41);
});
