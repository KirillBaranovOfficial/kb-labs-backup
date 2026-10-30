import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { createTestMatrix, parseAffectedPackages, parseAffectedSelection } from './remote-verify-plan.mjs';

const profile = JSON.parse(readFileSync(new URL('./verification-profile.json', import.meta.url), 'utf8'));

// case: S-262.7
test('verification profile owns each build and test stage definition exactly once', () => {
  const stageIds = Object.keys(profile.stages);
  const references = [profile.buildStage, profile.packageTestStage, ...Object.values(profile.independentTestStages).flat(), ...Object.values(profile.buildDependentTestStages).flat()];

  assert.ok(references.every((stage) => stageIds.includes(stage)));
  assert.ok(Number.isSafeInteger(profile.shardTimeoutMinutes) && profile.shardTimeoutMinutes > 0);
  for (const profileName of Object.keys(profile.independentTestStages)) {
    const assigned = [...profile.independentTestStages[profileName], ...profile.buildDependentTestStages[profileName]];
    assert.equal(new Set(assigned).size, assigned.length);
  }
});

// case: S-262.7
test('stream matrix uses the kb-devkit package set and registered stream suites', () => {
  const packages = parseAffectedPackages(JSON.stringify({ ok: true, data: { packages: ['@kb-labs/core', '@kb-labs/cli'] } }));
  const matrix = createTestMatrix(packages, profile.independentTestStages.stream, profile.buildDependentTestStages.stream, profile.packageTestStage, profile.maxParallel);

  assert.deepEqual(matrix.independent.include, [
    { stage: 'ci-script-suite' },
  ]);
  assert.deepEqual(matrix.dependent.include, [
    { stage: profile.packageTestStage, shard: 1, packages: ['@kb-labs/core'] },
    { stage: profile.packageTestStage, shard: 2, packages: ['@kb-labs/cli'] },
  ]);
});

// case: S-262.7
test('candidate profile separates output-dependent suites from independent suites', () => {
  const matrix = createTestMatrix(['@kb-labs/core'], profile.independentTestStages.candidate, profile.buildDependentTestStages.candidate, profile.packageTestStage, profile.maxParallel);

  assert.deepEqual(matrix.independent.include.map(({ stage }) => stage), profile.independentTestStages.candidate);
  assert.deepEqual(matrix.dependent.include.map(({ stage }) => stage), [
    ...profile.buildDependentTestStages.candidate,
    profile.packageTestStage,
  ]);
});

// case: S-262.7
test('candidate kb-create Go suite is planned after workspace build', () => {
  const independent = profile.independentTestStages.candidate;
  const dependent = profile.buildDependentTestStages.candidate;
  const kbCreatePackage = JSON.parse(readFileSync(new URL('../../tools/kb-create/package.json', import.meta.url), 'utf8')).name;
  const matrix = createTestMatrix([kbCreatePackage], independent, dependent, profile.packageTestStage, profile.maxParallel);

  assert.ok(!independent.includes('kb-create-go-suite'));
  assert.ok(dependent.includes('kb-create-go-suite'));
  assert.ok(!independent.includes('kb-devkit-check'));
  assert.ok(dependent.includes('kb-devkit-check'));
  assert.ok(matrix.dependent.include.some(({ stage }) => stage === 'kb-create-go-suite'));
});

// case: S-262.7
test('candidate selection includes all package-stage manifest identities while stream stays affected-only', () => {
  const affected = JSON.stringify({ ok: true, data: { packages: ['@kb-labs/core'], changedPathCount: 1 } });
  const candidate = parseAffectedSelection(affected, 'candidate');
  const stream = parseAffectedSelection(affected, 'stream');

  assert.ok(candidate.packages.includes('kb-create'));
  assert.deepEqual(stream.packages, ['@kb-labs/core']);
});

// case: S-262.7
test('package shards partition the kb-devkit package set once with even package counts', () => {
  const packages = Array.from({ length: 41 }, (_, index) => `@kb-labs/package-${index}`);
  const matrix = createTestMatrix(packages, [], [], profile.packageTestStage, profile.maxParallel);
  const shards = matrix.dependent.include;
  const assigned = shards.flatMap((shard) => shard.packages);
  const sizes = shards.map((shard) => shard.packages.length);

  assert.equal(shards.length, profile.maxParallel);
  assert.deepEqual([...assigned].sort(), [...packages].sort());
  assert.ok(Math.max(...sizes) - Math.min(...sizes) <= 1);
});

// case: S-262.7
test('rejects unsuccessful or malformed affected-package output instead of widening the test set', () => {
  assert.throws(() => parseAffectedPackages(JSON.stringify({ ok: false, error: { message: 'failed' } })), /package selection/);
  assert.throws(() => parseAffectedPackages(JSON.stringify({ ok: true, data: { packages: ['@kb-labs/core', '@kb-labs/core'] } })), /duplicate package/);
});

// case: S-262.7
test('rejects zero packages for a non-empty stream diff and for every candidate selection', () => {
  const emptyChangedStream = JSON.stringify({ ok: true, data: { packages: [], changedPathCount: 0 } });
  assert.deepEqual(parseAffectedSelection(emptyChangedStream, 'stream'), { packages: [], changedPathCount: 0, selectionReason: '' });
  assert.throws(
    () => parseAffectedSelection(JSON.stringify({ ok: true, data: { packages: [], changedPathCount: 3 } }), 'stream'),
    /KB_REMOTE_EMPTY_SELECTION/,
  );
  assert.ok(parseAffectedSelection(emptyChangedStream, 'candidate').packages.includes('kb-create'));
});

// case: S-262.7
test('carries the kb-devkit full-selection reason into prepare plan evidence', () => {
  const selection = JSON.stringify({
    ok: true,
    data: { packages: ['@kb-labs/core', '@kb-labs/cli'], changedPathCount: 2, selectionReason: 'full: unowned path tsconfig.base.json' },
  });
  assert.deepEqual(parseAffectedSelection(selection, 'stream'), {
    packages: ['@kb-labs/core', '@kb-labs/cli'],
    changedPathCount: 2,
    selectionReason: 'full: unowned path tsconfig.base.json',
  });
});
