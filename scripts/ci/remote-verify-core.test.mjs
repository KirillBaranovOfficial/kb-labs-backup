import test from 'node:test';
import assert from 'node:assert/strict';
import { RemoteOperationError, runRemoteVerification } from './remote-verify-core.mjs';

const sha = 'a'.repeat(40);
const pass = { schemaVersion: 1, sha, status: 'pass', jobs: [{ name: 'build', status: 'pass', failures: [], logTail: '' }] };
const run = (overrides = {}) => ({ id: 17, event: 'push', head_branch: 'verify/stream/20261025T120000Z-' + sha, head_sha: sha, path: '.github/workflows/remote-verify.yml', status: 'completed', conclusion: 'success', ...overrides });

function harness({ listRuns, getRun, downloadVerdict = async () => pass, deleteRef = async () => {}, resolveRef = async () => sha, failureJobs = async () => ({ jobs: [] }), failureLogs = async () => '' } = {}) {
  let time = 0;
  const calls = { pushes: [], deletes: [], lists: 0, gets: 0, ghTimes: [], remoteTimes: [], artifacts: 0 };
  const recordGh = () => { calls.ghTimes.push(time); calls.remoteTimes.push(time); };
  const recordGitRemote = () => calls.remoteTimes.push(time);
  const gh = {
    listRuns: async (...args) => { recordGh(); calls.lists += 1; return listRuns ? listRuns(...args) : [run({ head_branch: args[1] })]; },
    getRun: async (...args) => { recordGh(); calls.gets += 1; return getRun ? getRun(...args) : run(); },
    downloadVerdict: async (...args) => { recordGh(); calls.artifacts += 1; return downloadVerdict(...args); },
    failureJobs: async (...args) => { recordGh(); return failureJobs(...args); },
    failureLogs: async (...args) => { recordGh(); return failureLogs(...args); },
  };
  const git = {
    headSha: async () => sha,
    backupRemote: async () => 'git@github-kb-backup:owner/repo.git',
    pushRef: async (ref, pushedSha) => { recordGitRemote(); calls.pushes.push({ ref, sha: pushedSha }); },
    resolveRef: async (...args) => { recordGitRemote(); return resolveRef(...args); },
    deleteRef: async (ref) => { recordGitRemote(); calls.deletes.push(ref); return deleteRef(ref); },
  };
  const clock = { now: () => time, sleep: async (ms) => { time += ms; } };
  return { executors: { gh, git }, clock, calls, policy: { timeoutMs: 300_000, outageBudgetMs: 120_000, cleanupBudgetMs: 180_000 } };
}

function assertMinimumRemoteSpacing(times, minimumMs = 30_000) {
  for (let index = 1; index < times.length; index += 1) {
    assert.ok(times[index] - times[index - 1] >= minimumMs, `remote calls were ${times[index] - times[index - 1]}ms apart: ${times}`);
  }
}

function assertCause(cause, expected) {
  assert.ok(cause && typeof cause === 'object', 'terminal verdict is missing its structured cause');
  for (const field of ['operation', 'command', 'attempts', 'startedAtMs', 'endedAtMs', 'elapsedMs', 'lastError']) {
    assert.ok(Object.hasOwn(cause, field), `cause is missing ${field}`);
  }
  for (const [field, value] of Object.entries(expected)) assert.equal(cause[field], value, `cause.${field}`);
  assert.ok(cause.elapsedMs >= 0);
  assert.ok(cause.lastError.length <= 1_000);
}

// case: S-262.6
test('a transient GitHub poll failure retries and the recovered run can pass', async () => {
  const h = harness({ listRuns: (() => { let calls = 0; return async (_repo, branch) => { calls += 1; if (calls === 1) throw new RemoteOperationError('GitHub API request failed', { retryable: true }); return [run({ head_branch: branch })]; }; })() });
  const verdict = await runRemoteVerification({ stream: 'stream', out: '/tmp/verdict.json' }, h.executors, { clock: h.clock, policy: h.policy });
  assert.equal(verdict.status, 'pass', JSON.stringify(verdict));
  assert.equal(h.calls.lists, 2);
  assert.equal(h.calls.pushes.length, 1);
  assertMinimumRemoteSpacing(h.calls.remoteTimes);
});

test('persistent GitHub failures exhaust the outage budget and classify as outage', async () => {
  const h = harness({ listRuns: async () => { throw new RemoteOperationError('transport unavailable', { retryable: true }); } });
  const verdict = await runRemoteVerification({ stream: 'stream', out: '/tmp/verdict.json' }, h.executors, { clock: h.clock, policy: { ...h.policy, timeoutMs: 600_000, outageBudgetMs: 300_000 } });
  assert.equal(verdict.status, 'outage');
  assertCause(verdict.cause, { operation: 'run_lookup', command: `repos/owner/repo/actions/runs?branch=verify%2Fstream%2F19700101T000000Z-${sha}&event=push&per_page=100`, attempts: 5, lastError: 'transport unavailable' });
  assert.equal(h.calls.deletes.length, 1);
  assertMinimumRemoteSpacing(h.calls.remoteTimes);
  for (let index = 1; index < h.calls.ghTimes.length; index += 1) {
    const gap = h.calls.ghTimes[index] - h.calls.ghTimes[index - 1];
    assert.ok(gap >= 30_000 && gap <= 60_000, `retry gap was ${gap}ms: ${h.calls.ghTimes}`);
  }
});

test('an empty run queue at the overall deadline classifies as timeout', async () => {
  const h = harness({ listRuns: async () => [] });
  h.policy.timeoutMs = 2500;
  const verdict = await runRemoteVerification({ stream: 'stream', out: '/tmp/verdict.json' }, h.executors, { clock: h.clock, policy: h.policy });
  assert.equal(verdict.status, 'timeout');
  assertCause(verdict.cause, { operation: 'run_lookup', command: `repos/owner/repo/actions/runs?branch=verify%2Fstream%2F19700101T000000Z-${sha}&event=push&per_page=100`, attempts: 0, lastError: 'Remote verification deadline expired' });
});

test('a rejected push is a fail verdict with its exact operation cause, not an outage', async () => {
  const h = harness();
  h.executors.git.pushRef = async () => { throw new RemoteOperationError('remote rejected: protected branch', { retryable: false }); };
  const verdict = await runRemoteVerification({ stream: 'stream', out: '/tmp/verdict.json' }, h.executors, { clock: h.clock, policy: h.policy });
  assert.equal(verdict.status, 'fail');
  assertCause(verdict.cause, { operation: 'push_ref', command: `git push backup ${sha}:refs/heads/verify/stream/19700101T000000Z-${sha}`, attempts: 1, lastError: 'remote rejected: protected branch' });
  assert.equal(h.calls.lists, 0);
});

// case: S-262.6
test('a GitHub run conclusion of timed_out classifies as timeout', async () => {
  const h = harness({ listRuns: async (_repo, branch) => [run({ head_branch: branch, conclusion: 'timed_out' })] });
  const verdict = await runRemoteVerification({ stream: 'stream', out: '/tmp/verdict.json' }, h.executors, { clock: h.clock, policy: h.policy });
  assert.equal(verdict.status, 'timeout');
  assertCause(verdict.cause, { operation: 'workflow_result', command: 'repos/owner/repo/actions/runs/17', attempts: 1, lastError: 'GitHub Actions run 17 concluded timed_out' });
});

// case: S-262.6
test('reattaching by run ID does not push and binds the artifact to run head_sha', async () => {
  let lookups = 0;
  const h = harness({ getRun: async () => { lookups += 1; return run({ status: lookups === 1 ? 'in_progress' : 'completed' }); } });
  const verdict = await runRemoteVerification({ attach: '17', out: '/tmp/verdict.json' }, h.executors, { clock: h.clock, policy: h.policy });
  assert.equal(verdict.status, 'pass');
  assert.equal(h.calls.pushes.length, 0);
  assert.equal(h.calls.gets, 2);
  assertMinimumRemoteSpacing(h.calls.remoteTimes);
  assert.equal(h.calls.artifacts, 1);
});

// case: S-262.6
test('reattaching by verify ref resolves its SHA, selects that push run, and does not push', async () => {
  const ref = `verify/stream/20261025T120000Z-${sha}`;
  const h = harness({
    listRuns: async (_repo, branch) => [run({ head_branch: branch })],
    resolveRef: async (_remote, requestedRef) => {
      assert.equal(requestedRef, ref);
      return sha;
    },
  });
  const verdict = await runRemoteVerification({ attach: ref, out: '/tmp/verdict.json' }, h.executors, { clock: h.clock, policy: h.policy });
  assert.equal(verdict.status, 'pass');
  assert.equal(h.calls.pushes.length, 0);
  assert.equal(h.calls.lists, 1);
  assertMinimumRemoteSpacing(h.calls.remoteTimes);
  assert.equal(h.calls.artifacts, 1);
});

// case: S-262.6
test('persistent ref cleanup failure is retried and remains a non-fatal diagnostic', async () => {
  let deletes = 0;
  const h = harness({ deleteRef: async () => { deletes += 1; throw new RemoteOperationError('git transport reset', { retryable: true }); } });
  const verdict = await runRemoteVerification({ stream: 'stream', out: '/tmp/verdict.json' }, h.executors, { clock: h.clock, policy: h.policy });
  assert.equal(verdict.status, 'pass');
  assert.ok(deletes > 1);
  assertMinimumRemoteSpacing(h.calls.remoteTimes);
  assert.match(verdict.diagnostics.join('\n'), /scheduled sweep/);
  assertCause(verdict.cause, { operation: 'delete_ref', attempts: 3, lastError: 'git transport reset' });
});

// case: S-262.6
test('cleanup retries a transient failure and succeeds without a warning', async () => {
  let deletes = 0;
  const h = harness({ deleteRef: async () => { deletes += 1; if (deletes === 1) throw new RemoteOperationError('connection reset', { retryable: true }); } });
  const verdict = await runRemoteVerification({ stream: 'stream', out: '/tmp/verdict.json' }, h.executors, { clock: h.clock, policy: h.policy });
  assert.equal(verdict.status, 'pass');
  assert.equal(deletes, 2);
  assertMinimumRemoteSpacing(h.calls.remoteTimes);
  assert.deepEqual(verdict.diagnostics, [], `deletes=${deletes}, fakeTime=${h.clock.now()}`);
});

// case: S-262.6
test('poll and retry cadence clamp to 30–60 seconds and default to 45 seconds', async () => {
  const cases = [
    { requested: undefined, expected: 45_000 },
    { requested: 1_000, expected: 30_000 },
    { requested: 90_000, expected: 60_000 },
  ];
  for (const { requested, expected } of cases) {
    const h = harness({ listRuns: (() => { let count = 0; return async (_repo, branch) => { count += 1; return count === 1 ? [] : [run({ head_branch: branch })]; }; })() });
    const policy = { ...h.policy, ...(requested === undefined ? {} : { pollMs: requested }) };
    const verdict = await runRemoteVerification({ stream: 'stream', out: '/tmp/verdict.json' }, h.executors, { clock: h.clock, policy });
    assert.equal(verdict.status, 'pass');
    assert.equal(h.calls.ghTimes[1] - h.calls.ghTimes[0], expected);
    assertMinimumRemoteSpacing(h.calls.remoteTimes);
    assert.equal(h.calls.artifacts, 1);
  }
});

// case: S-262.6
test('API calls remain bounded by the deadline and minimum cadence', async () => {
  let polls = 0;
  const h = harness({ listRuns: async () => { polls += 1; if (polls > 10) throw new Error('fake clock did not advance between calls'); return []; } });
  const timeoutMs = 180_000;
  const verdict = await runRemoteVerification({ stream: 'stream', out: '/tmp/verdict.json' }, h.executors, {
    clock: h.clock,
    policy: { ...h.policy, timeoutMs, pollMs: 30_000 },
  });
  assert.equal(verdict.status, 'timeout');
  assert.ok(h.calls.ghTimes.length <= Math.ceil(timeoutMs / 30_000), `API calls=${h.calls.ghTimes.length}, times=${h.calls.ghTimes}`);
  assertMinimumRemoteSpacing(h.calls.remoteTimes);
});

// case: S-262.6
test('default cleanup budget permits retrying a transient delete failure', async () => {
  let deletes = 0;
  const h = harness({ deleteRef: async () => { deletes += 1; if (deletes === 1) throw new RemoteOperationError('connection reset', { retryable: true }); } });
  const { cleanupBudgetMs: _omitted, ...policy } = h.policy;
  const verdict = await runRemoteVerification({ stream: 'stream', out: '/tmp/verdict.json' }, h.executors, { clock: h.clock, policy });
  assert.equal(verdict.status, 'pass');
  assert.equal(deletes, 2);
  assert.deepEqual(verdict.diagnostics, []);
});

// case: S-262.6
test('an artifact SHA mismatch cannot produce a passing verdict', async () => {
  const h = harness({ downloadVerdict: async () => ({ ...pass, sha: 'b'.repeat(40) }) });
  const verdict = await runRemoteVerification({ stream: 'stream', out: '/tmp/verdict.json' }, h.executors, { clock: h.clock, policy: h.policy });
  assert.equal(verdict.status, 'fail');
  assertCause(verdict.cause, { operation: 'artifact_validation', command: 'remote-verdict artifact for run 17' });
  assert.match(verdict.diagnostics.join('\n'), /SHA mismatch/);
});

// case: S-262.6
test('a failed run fetches its artifact once and spaces both diagnostics calls', async () => {
  const h = harness({
    listRuns: async (_repo, branch) => [run({ head_branch: branch, conclusion: 'failure' })],
    downloadVerdict: async () => { throw new RemoteOperationError('artifact unavailable', { retryable: true }); },
    failureJobs: async () => ({ jobs: [{ name: 'unit', conclusion: 'failure', steps: [{ name: 'test example', conclusion: 'failure' }] }] }),
    failureLogs: async () => 'bounded failure log',
  });
  const verdict = await runRemoteVerification({ stream: 'stream', out: '/tmp/verdict.json' }, h.executors, { clock: h.clock, policy: h.policy });
  assert.equal(verdict.status, 'fail');
  assertCause(verdict.cause, { operation: 'workflow_result', command: 'repos/owner/repo/actions/runs/17' });
  assert.equal(h.calls.artifacts, 1);
  assert.deepEqual(verdict.jobs[0].failures, ['test example']);
  assert.equal(verdict.jobs[0].logTail, 'bounded failure log');
  assertMinimumRemoteSpacing(h.calls.remoteTimes);
});
