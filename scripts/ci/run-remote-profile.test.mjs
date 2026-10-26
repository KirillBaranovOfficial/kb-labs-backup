import test from 'node:test';
import assert from 'node:assert/strict';
import { runProfile } from './remote-profile.mjs';

const stage = (name) => ({ name, command: name, args: [] });

// case: S-262.7
test('stream profile stops at the first failure and records elapsed milliseconds', async () => {
  const calls = [];
  let now = 100;
  const jobs = await runProfile({
    profile: 'stream',
    stages: [stage('build'), stage('tests'), stage('check')],
    runCommand: async (command) => {
      calls.push(command);
      now += 125;
      return { status: command === 'tests' ? 1 : 0, stdout: '', stderr: '' };
    },
    now: () => now,
  });

  assert.deepEqual(calls, ['build', 'tests']);
  assert.deepEqual(jobs.map(({ name, status, durationMs }) => ({ name, status, durationMs })), [
    { name: 'build', status: 'pass', durationMs: 125 },
    { name: 'tests', status: 'fail', durationMs: 125 },
  ]);
});

// case: S-262.7
test('candidate profile runs all stages after failure and records every duration', async () => {
  const calls = [];
  let now = 0;
  const jobs = await runProfile({
    profile: 'candidate',
    stages: [stage('build'), stage('tests'), stage('check')],
    runCommand: async (command) => {
      calls.push(command);
      now += 40;
      return { status: command === 'tests' ? 1 : 0, stdout: '', stderr: '' };
    },
    now: () => now,
  });

  assert.deepEqual(calls, ['build', 'tests', 'check']);
  assert.deepEqual(jobs.map(({ status, durationMs }) => ({ status, durationMs })), [
    { status: 'pass', durationMs: 40 },
    { status: 'fail', durationMs: 40 },
    { status: 'pass', durationMs: 40 },
  ]);
});

// case: S-262.7
test('rejects an unknown profile instead of silently choosing a policy', async () => {
  await assert.rejects(() => runProfile({ profile: 'unknown', stages: [], runCommand: async () => ({}), now: Date.now }), /Unknown verification profile/);
});
