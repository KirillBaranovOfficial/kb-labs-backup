import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { load } from 'js-yaml';
import { parseVerdict, repositoryFromRemote, selectVerificationRun, terminalVerdict } from './remote-verdict.mjs';

const good = { schemaVersion: 1, sha: 'abc123', status: 'pass', jobs: [{ name: 'build', status: 'pass', failures: [], logTail: '' }] };
const cause = { operation: 'push_ref', command: 'git push backup sha:refs/heads/verify/stream', attempts: 1, startedAtMs: 10, endedAtMs: 15, elapsedMs: 5, lastError: 'remote rejected' };

test('accepts a verdict only for the requested SHA', () => {
  assert.equal(parseVerdict(good, 'abc123'), good);
  assert.throws(() => parseVerdict(good, 'def456'), /SHA mismatch/);
});

test('rejects pass when any job failed', () => {
  assert.throws(() => parseVerdict({ ...good, cause, jobs: [{ name: 'build', status: 'fail', failures: [{ name: 'pkg › test', message: 'TS failed' }], logTail: 'TS failed' }] }, 'abc123'), /non-passing job/);
});

test('rejects a failing stage without a named failure unit', () => {
  assert.throws(() => parseVerdict({ ...good, status: 'fail', cause, jobs: [{ name: 'tests', status: 'fail', failures: [], logTail: '' }] }, 'abc123'), /failed without naming a failure/);
});

test('rejects verdicts above the diagnostic size limit', () => {
  const oversized = { ...good, diagnostics: ['x'.repeat(64_000)] };
  assert.throws(() => parseVerdict(oversized, 'abc123'), /64 KB limit/);
});

test('every terminal failure class carries validated operation cause fields', () => {
  for (const status of ['fail', 'timeout', 'outage']) {
    const verdict = terminalVerdict('abc123', status, cause, `${status} result`);
    assert.equal(verdict.status, status);
    assert.equal(parseVerdict(verdict, 'abc123').cause, cause);
  }
  assert.throws(() => parseVerdict({ ...good, status: 'fail' }, 'abc123'), /missing its structured cause/);
  assert.throws(() => terminalVerdict('abc123', 'pass', cause, 'unexpected'), /Invalid terminal/);
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
