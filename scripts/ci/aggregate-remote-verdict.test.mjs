import assert from 'node:assert/strict';
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { aggregateVerdict, fetchGitHubJobEvidence } from './aggregate-remote-verdict.mjs';

const profile = JSON.parse(readFileSync(new URL('./verification-profile.json', import.meta.url), 'utf8'));
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const passingPrepare = () => ({
  name: 'prepare', status: 'pass', selectedPackageCount: 41, failures: [], logTail: '', durationMs: 100,
});

// case: S-262.7
test('aggregates every shard with the existing verdict shape and per-stage timing', () => {
  const verdict = aggregateVerdict({
    sha: 'a'.repeat(40),
    prepare: passingPrepare(),
    build: { name: profile.buildStage, status: 'pass', selectedPackageCount: 41, failures: [], logTail: '', durationMs: 300 },
    matrix: { include: [{ stage: profile.buildDependentTestStages.candidate[0] }, { stage: profile.packageTestStage, shard: 1, packages: ['@kb-labs/core'] }] },
    results: [
      { name: profile.buildDependentTestStages.candidate[0], status: 'pass', selectedPackageCount: 41, failures: [], logTail: '', durationMs: 70 },
      { name: `${profile.packageTestStage}:shard-1`, status: 'fail', selectedPackageCount: 1, failures: ['test failed'], logTail: 'test failed', durationMs: 40 },
    ],
    selectedPackageCount: 41, prepareJobResult: 'success', buildJobResult: 'success',
    githubJobs: [
      { name: `Build-dependent test ${profile.buildDependentTestStages.candidate[0]}`, conclusion: 'success', steps: [], annotations: [] },
      { name: `Build-dependent test 1`, conclusion: 'failure', steps: [], annotations: [] },
    ], verificationProfile: 'candidate',
  });

  assert.equal(verdict.schemaVersion, 1);
  assert.equal(verdict.status, 'fail');
  assert.deepEqual(verdict.jobs.map(({ name }) => name), ['prepare', profile.buildStage, profile.buildDependentTestStages.candidate[0], `${profile.packageTestStage}:shard-1`]);
  assert.deepEqual(verdict.jobs.map(({ durationMs }) => durationMs), [100, 300, 70, 40]);
  assert.deepEqual(verdict.jobs.map(({ selectedPackageCount }) => selectedPackageCount), [41, 41, 41, 1]);
});

// case: S-262.7
test('marks matrix shards cancelled by stream fail-fast as skipped, never as a passing verdict', () => {
  const verdict = aggregateVerdict({
    sha: 'b'.repeat(40),
    prepare: passingPrepare(),
    build: { name: profile.buildStage, status: 'pass', selectedPackageCount: 41, failures: [], logTail: '', durationMs: 300 },
    matrix: { include: [{ stage: profile.packageTestStage, shard: 1, packages: ['@kb-labs/a'] }, { stage: profile.packageTestStage, shard: 2, packages: ['@kb-labs/b'] }] },
    results: [{ name: `${profile.packageTestStage}:shard-1`, status: 'fail', selectedPackageCount: 1, failures: ['failed'], logTail: 'failed', durationMs: 70 }],
    githubJobs: [
      { name: 'Build-dependent test 1', conclusion: 'failure', steps: [], annotations: [] },
      { name: 'Build-dependent test 2', conclusion: 'cancelled', steps: [], annotations: [] },
    ],
    selectedPackageCount: 41, prepareJobResult: 'success', buildJobResult: 'success', verificationProfile: 'stream',
  });

  assert.equal(verdict.status, 'fail');
  assert.equal(verdict.jobs[3].name, `${profile.packageTestStage}:shard-2`);
  assert.equal(verdict.jobs[3].status, 'skipped');
  assert.match(verdict.jobs[3].failures[0], /cancelled by fail-fast/);
  assert.equal(verdict.jobs[3].selectedPackageCount, 1);
});


// case: S-262.7
test('aggregates the prepare stage and fails with its named cause', () => {
  const prepare = {
    name: 'prepare',
    status: 'fail',
    selectedPackageCount: 0,
    failures: ['install-workspace-dependencies exited with status 17'],
    logTail: 'pnpm registry unavailable',
    durationMs: 1250,
    cache: { pnpm: 'miss-or-restore-prefix', go: 'exact-hit', devkit: 'unavailable' },
  };
  const verdict = aggregateVerdict({
    sha: 'c'.repeat(40), prepare, prepareJobResult: 'failure', build: undefined, buildJobResult: 'skipped', matrix: { include: [] }, results: [], selectedPackageCount: 0,
  });

  assert.equal(verdict.status, 'fail');
  assert.deepEqual(verdict.jobs.map(({ name }) => name), ['prepare', profile.buildStage]);
  assert.equal(verdict.jobs[0].status, 'fail');
  assert.equal(verdict.jobs[0].durationMs, 1250);
  assert.deepEqual(verdict.jobs[0].failures, [...prepare.failures, 'GitHub job prepare concluded failure.']);
  assert.match(verdict.jobs[0].logTail, /pnpm registry unavailable/);
});

// case: S-262.7
test('selection reason is preserved in prepare verdict evidence', () => {
  const prepare = { ...passingPrepare(), selectionReason: 'full: unowned path tsconfig.base.json' };
  const verdict = aggregateVerdict({
    sha: 'f'.repeat(40), prepare, prepareJobResult: 'success', build: undefined, buildJobResult: 'success', matrix: { include: [] }, results: [], selectedPackageCount: 41,
  });
  assert.equal(verdict.jobs[0].selectionReason, 'full: unowned path tsconfig.base.json');
});

// case: S-262.7
test('missing build result after runner shutdown retains the last persisted resource sample', () => {
  const sample = {
    sampledAt: '2026-10-26T23:51:45.000Z',
    memory: { totalBytes: 16 * 1024 ** 3, availableBytes: 512 * 1024 ** 2, usedBytes: 15.5 * 1024 ** 3 },
    disk: { availableBytes: 5 * 1024 ** 3, workspace: '/home/runner/work/repository' },
    topRss: [{ pid: 100, rssBytes: 8 * 1024 ** 3, command: 'node' }],
  };
  const verdict = aggregateVerdict({
    sha: 'e'.repeat(40), prepare: passingPrepare(), prepareJobResult: 'success', build: undefined, buildJobResult: 'failure',
    buildResourceSamples: [sample], matrix: { include: [] }, results: [], selectedPackageCount: 160,
  });

  assert.equal(verdict.status, 'fail');
  assert.equal(verdict.jobs[1].name, profile.buildStage);
  assert.equal(verdict.jobs[1].status, 'fail');
  assert.ok(verdict.jobs[1].failures.some((failure) => failure.includes('runner-shutdown')));
  assert.deepEqual(verdict.jobs[1].lastResourceSample, sample);
  assert.equal(verdict.jobs[1].resourcePeaks.peakMemoryBytes, sample.memory.usedBytes);
  assert.equal(verdict.jobs[1].resourcePeaks.minimumFreeDiskBytes, sample.disk.availableBytes);
});

// case: S-262.7
test('real prepare result producer records a named command failure for aggregation', () => {
  const temporaryRoot = mkdtempSync(path.join(os.tmpdir(), 'remote-prepare-result-'));
  const binDir = path.join(temporaryRoot, 'bin');
  const preparePath = path.join(temporaryRoot, 'prepare.json');
  const fakeCorepack = path.join(binDir, 'corepack');
  try {
    mkdirSync(binDir, { recursive: true });
    writeFileSync(fakeCorepack, '#!/bin/sh\nprintf "registry unavailable\n"\nexit 17\n');
    chmodSync(fakeCorepack, 0o755);
    const result = spawnSync(process.execPath, ['scripts/ci/run-remote-prepare.mjs'], {
      cwd: root,
      encoding: 'utf8',
      env: {
        ...process.env,
        PATH: `${binDir}:${process.env.PATH}`,
        VERIFICATION_PROFILE: 'stream',
        DEFAULT_BRANCH: 'main',
        REMOTE_PREPARE_RESULT: preparePath,
        REMOTE_AFFECTED_PACKAGES: path.join(temporaryRoot, 'affected.json'),
        REMOTE_TEST_PLAN: path.join(temporaryRoot, 'plan.json'),
        GITHUB_OUTPUT: path.join(temporaryRoot, 'github-output'),
        PNPM_CACHE_HIT: 'false',
        GO_CACHE_HIT: 'true',
      },
    });
    assert.equal(result.status, 1, result.stderr);
    const prepare = JSON.parse(readFileSync(preparePath, 'utf8'));
    const verdict = aggregateVerdict({
      sha: 'd'.repeat(40), prepare, prepareJobResult: 'failure', build: undefined, buildJobResult: 'skipped', matrix: { include: [] }, results: [], selectedPackageCount: 0,
    });

    assert.equal(prepare.name, 'prepare');
    assert.equal(prepare.status, 'fail');
    assert.ok(prepare.durationMs >= 0);
    assert.equal(prepare.selectedPackageCount, 0);
    assert.deepEqual(prepare.cache, { pnpm: 'miss-or-restore-prefix', go: 'exact-hit', devkit: 'unavailable' });
    assert.equal(prepare.operations[0].name, 'install-workspace-dependencies');
    assert.equal(prepare.operations[0].status, 'fail');
    assert.ok(prepare.failures.some((failure) => failure.includes('install-workspace-dependencies')));
    assert.match(prepare.logTail, /registry unavailable/);
    assert.equal(verdict.jobs[0].name, 'prepare');
    assert.equal(verdict.jobs[0].status, 'fail');
    assert.ok(verdict.jobs[0].failures.some((failure) => failure.includes('install-workspace-dependencies')));
  } finally {
    rmSync(temporaryRoot, { recursive: true, force: true });
  }
});

// case: S-262.7
test('missing shard artifacts retain GitHub runner-loss conclusion, failed steps, and annotations', () => {
  const shard = { stage: profile.buildDependentTestStages.candidate[0] };
  const verdict = aggregateVerdict({
    sha: 'a'.repeat(40), prepare: passingPrepare(),
    build: { name: profile.buildStage, status: 'pass', selectedPackageCount: 41, failures: [], logTail: '' },
    matrix: { include: [shard] }, results: [], selectedPackageCount: 41, prepareJobResult: 'success', buildJobResult: 'success',
    githubJobs: [{
      name: `Build-dependent test ${shard.stage}`, conclusion: 'failure',
      steps: [{ name: 'Run build-dependent test', conclusion: 'in_progress' }],
      annotations: [{ message: 'The hosted runner lost communication with the server.' }],
    }], verificationProfile: 'candidate',
  });

  const job = verdict.jobs.at(-1);
  assert.equal(job.status, 'fail');
  assert.equal(job.githubConclusion, 'failure');
  assert.deepEqual(job.steps, [{ name: 'Run build-dependent test', conclusion: 'in_progress' }]);
  assert.match(job.failures.join('\n'), /lost communication with the server/);
  assert.match(job.logTail, /in_progress/);
});

// case: S-262.7
test('only a cancelled peer in the same fail-fast matrix is reported as skipped by fail-fast', () => {
  const stage = profile.packageTestStage;
  const verdict = aggregateVerdict({
    sha: 'b'.repeat(40), prepare: passingPrepare(),
    build: { name: profile.buildStage, status: 'pass', selectedPackageCount: 41, failures: [], logTail: '' },
    matrix: { include: [{ stage, shard: 1, packages: ['@kb-labs/a'] }, { stage, shard: 2, packages: ['@kb-labs/b'] }] },
    results: [{ name: `${stage}:shard-1`, status: 'fail', selectedPackageCount: 1, failures: ['test failed'], logTail: 'test failed' }],
    selectedPackageCount: 41, prepareJobResult: 'success', buildJobResult: 'success',
    githubJobs: [{ name: `Build-dependent test 2`, conclusion: 'cancelled', steps: [], annotations: [] }],
    verificationProfile: 'stream',
  });

  assert.equal(verdict.jobs.at(-1).status, 'skipped');
  assert.deepEqual(verdict.jobs.at(-1).failures, ['cancelled by fail-fast after a peer in the same test matrix failed']);
  assert.equal(verdict.jobs.at(-1).logTail, 'cancelled by fail-fast after a peer in the same test matrix failed');
});

// case: S-262.7
test('a missing shard with GitHub timed_out conclusion is a named timeout', () => {
  const stage = profile.buildDependentTestStages.candidate[0];
  const verdict = aggregateVerdict({
    sha: 'c'.repeat(40), prepare: passingPrepare(),
    build: { name: profile.buildStage, status: 'pass', selectedPackageCount: 41, failures: [], logTail: '' },
    matrix: { include: [{ stage }] }, results: [], selectedPackageCount: 41, prepareJobResult: 'success', buildJobResult: 'success',
    githubJobs: [{ name: `Build-dependent test ${stage}`, conclusion: 'timed_out', steps: [], annotations: [] }],
    verificationProfile: 'candidate',
  });

  assert.equal(verdict.jobs.at(-1).name, stage);
  assert.equal(verdict.jobs.at(-1).status, 'timeout');
  assert.match(verdict.jobs.at(-1).failures.join('\n'), /timed out/);
});

// case: S-262.7
test('GitHub job evidence API failure names the missing shard and API error', () => {
  const stage = profile.buildDependentTestStages.candidate[0];
  const verdict = aggregateVerdict({
    sha: 'd'.repeat(40), prepare: passingPrepare(),
    build: { name: profile.buildStage, status: 'pass', selectedPackageCount: 41, failures: [], logTail: '' },
    matrix: { include: [{ stage }] }, results: [], selectedPackageCount: 41, prepareJobResult: 'success', buildJobResult: 'success',
    githubJobsError: 'gh api: HTTP 403',
  });

  assert.equal(verdict.jobs.at(-1).status, 'fail');
  assert.match(verdict.jobs.at(-1).failures.join('\n'), /GitHub job evidence API read failed: gh api: HTTP 403/);
});


// case: S-262.7
test('GitHub job evidence fetch reads job steps and annotations from the Actions API', () => {
  const calls = [];
  const evidence = fetchGitHubJobEvidence({
    repository: 'kb-labs/workspace', runId: '42',
    plannedJobs: [{ stage: profile.buildDependentTestStages.candidate[0] }],
    runCommand: (endpoint) => {
      calls.push(endpoint);
      if (endpoint.endsWith('/jobs?per_page=100')) return JSON.stringify({ jobs: [{
        name: 'Build-dependent test journey-unit-suite', conclusion: 'failure', steps: [{ name: 'test', conclusion: 'in_progress' }],
        check_run_url: 'https://api.github.com/repos/kb-labs/workspace/check-runs/123',
      }] });
      return JSON.stringify([{ message: 'hosted runner lost communication' }]);
    },
  });

  assert.equal(calls.length, 2);
  assert.match(calls[0], /actions\/runs\/42\/jobs/);
  assert.match(calls[1], /check-runs\/123\/annotations/);
  assert.equal(evidence[0].steps[0].conclusion, 'in_progress');
  assert.equal(evidence[0].annotations[0].message, 'hosted runner lost communication');
});

// case: S-262.7
test('a passing build stage artifact cannot override a failed enclosing build job', () => {
  const verdict = aggregateVerdict({
    sha: 'e'.repeat(40), prepare: passingPrepare(), prepareJobResult: 'success',
    build: { name: profile.buildStage, status: 'pass', selectedPackageCount: 0, failures: [], logTail: '', durationMs: 12 },
    buildJobResult: 'failure',
    githubJobs: [{
      name: 'build', conclusion: 'failure',
      steps: [{ name: 'Upload workspace build artifact', conclusion: 'failure', status: 'completed' }],
      annotations: [{ message: 'Artifact upload failed.' }],
    }],
    matrix: { include: [] }, results: [], selectedPackageCount: 0,
  });

  assert.equal(verdict.status, 'fail');
  assert.equal(verdict.jobs[1].status, 'fail');
  assert.match(verdict.jobs[1].failures.join('\n'), /GitHub job build concluded failure/);
  assert.match(verdict.jobs[1].failures.join('\n'), /Upload workspace build artifact/);
  assert.match(verdict.jobs[1].failures.join('\n'), /Artifact upload failed/);
});

// case: S-262.7
test('a passing matrix stage artifact cannot override a failed enclosing shard job', () => {
  const stage = profile.buildDependentTestStages.candidate[0];
  const verdict = aggregateVerdict({
    sha: 'f'.repeat(40), prepare: passingPrepare(), prepareJobResult: 'success',
    build: { name: profile.buildStage, status: 'pass', selectedPackageCount: 41, failures: [], logTail: '' },
    buildJobResult: 'success',
    matrix: { include: [{ stage }] },
    results: [{ name: stage, status: 'pass', selectedPackageCount: 41, failures: [], logTail: '', durationMs: 22 }],
    selectedPackageCount: 41,
    githubJobs: [{
      name: `Build-dependent test ${stage}`, conclusion: 'failure',
      steps: [{ name: 'Upload build-dependent result', conclusion: 'failure', status: 'completed' }],
      annotations: [{ message: 'Result artifact upload failed.' }],
    }], verificationProfile: 'candidate',
  });

  const job = verdict.jobs.at(-1);
  assert.equal(verdict.status, 'fail');
  assert.equal(job.status, 'fail');
  assert.match(job.failures.join('\n'), /GitHub job .* concluded failure/);
  assert.match(job.failures.join('\n'), /Upload build-dependent result/);
  assert.match(job.failures.join('\n'), /Result artifact upload failed/);
});


// case: S-262.7
test('a planned job with no readable GitHub conclusion fails by name', () => {
  const verdict = aggregateVerdict({
    sha: '9'.repeat(40), prepare: passingPrepare(), prepareJobResult: 'success',
    build: { name: profile.buildStage, status: 'pass', selectedPackageCount: 0, failures: [], logTail: '' },
    buildJobResult: 'unknown', githubJobsError: 'gh api: HTTP 403',
    matrix: { include: [] }, results: [], selectedPackageCount: 0,
  });

  assert.equal(verdict.jobs[1].status, 'fail');
  assert.match(verdict.jobs[1].failures.join('\n'), /GitHub job evidence API read failed: gh api: HTTP 403/);
});
