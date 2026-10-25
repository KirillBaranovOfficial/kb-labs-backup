import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { load } from 'js-yaml';
import { parseVerdict, repositoryFromRemote, selectVerificationRun, terminalVerdict } from './remote-verdict.mjs';

const good = { schemaVersion: 1, sha: 'abc123', status: 'pass', jobs: [{ name: 'build', status: 'pass', failures: [], logTail: '' }] };

test('accepts a verdict only for the requested SHA', () => {
  assert.equal(parseVerdict(good, 'abc123'), good);
  assert.throws(() => parseVerdict(good, 'def456'), /SHA mismatch/);
});

test('rejects pass when any job failed', () => {
  assert.throws(() => parseVerdict({ ...good, jobs: [{ name: 'build', status: 'fail', failures: [{ name: 'pkg › test', message: 'TS failed' }], logTail: 'TS failed' }] }, 'abc123'), /non-passing job/);
});

test('rejects a failing stage without a named failure unit', () => {
  assert.throws(() => parseVerdict({ ...good, status: 'fail', jobs: [{ name: 'tests', status: 'fail', failures: [], logTail: '' }] }, 'abc123'), /failed without naming a failure/);
});

test('rejects verdicts above the diagnostic size limit', () => {
  const oversized = { ...good, diagnostics: ['x'.repeat(64_000)] };
  assert.throws(() => parseVerdict(oversized, 'abc123'), /64 KB limit/);
});

test('classifies bounded wait expiry and API outages distinctly', () => {
  assert.equal(terminalVerdict('abc123', 'timeout', 'run wait expired').status, 'timeout');
  assert.equal(terminalVerdict('abc123', 'outage', 'GitHub API unavailable').status, 'outage');
  assert.throws(() => terminalVerdict('abc123', 'pass', 'unexpected'), /Invalid terminal/);
});

test('derives the repository from the configured SSH alias and HTTPS remotes', () => {
  assert.equal(repositoryFromRemote('git@github-kb-backup:KirillBaranovOfficial/kb-labs-backup.git'), 'KirillBaranovOfficial/kb-labs-backup');
  assert.equal(repositoryFromRemote('https://github.com/kb-labs-team/kb-labs.git'), 'kb-labs-team/kb-labs');
});

test('selects only the push run for the exact branch, workflow, and SHA', () => {
  const runs = [
    { id: 1, event: 'push', head_branch: 'verify/stream', head_sha: 'other', path: '.github/workflows/remote-verify.yml' },
    { id: 2, event: 'workflow_dispatch', head_branch: 'verify/stream', head_sha: 'abc123', path: '.github/workflows/remote-verify.yml' },
    { id: 3, event: 'push', head_branch: 'verify/other', head_sha: 'abc123', path: '.github/workflows/remote-verify.yml' },
    { id: 4, event: 'push', head_branch: 'verify/stream', head_sha: 'abc123', path: '.github/workflows/other.yml' },
    { id: 5, event: 'push', head_branch: 'verify/stream', head_sha: 'abc123', path: '.github/workflows/remote-verify.yml' },
  ];
  assert.equal(selectVerificationRun(runs, { branch: 'verify/stream', sha: 'abc123', workflowPath: '.github/workflows/remote-verify.yml' }).id, 5);
});

test('remote workflow uploads the verdict artifact even when its profile step fails', () => {
  const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
  const workflow = load(readFileSync(path.join(root, '.github/workflows/remote-verify.yml'), 'utf8'));
  const steps = workflow.jobs.verify.steps;
  const upload = steps.find((step) => step.name === 'Upload commit-bound verdict');
  assert.ok(upload, 'workflow is missing the commit-bound verdict upload');
  assert.equal(upload.if, 'always()');
  assert.equal(upload.with.path, 'verdict.json');
});
