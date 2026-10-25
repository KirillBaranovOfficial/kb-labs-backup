import test from 'node:test';
import assert from 'node:assert/strict';
import { existsSync, mkdtempSync, mkdirSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import {
  checkFailures,
  boundFailureUnits,
  goTestFailures,
  journeyFailures,
  vitestFailures,
} from './remote-stage-results.mjs';
import * as stageResults from './remote-stage-results.mjs';
import { assertStageAdaptersMatchProfile, stageAdapters } from './remote-profile-stage-adapters.mjs';
import { runProfileStages } from './remote-profile-execution.mjs';
import { parseVerdict } from './remote-verdict.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');

function fixture(t, prefix) {
  const dir = mkdtempSync(path.join(os.tmpdir(), prefix));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  return dir;
}

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { encoding: 'utf8', ...options });
  assert.equal(result.error, undefined, result.error?.message);
  return result;
}

// case: S-262.6
test('decodes reports emitted by the installed Vitest runner', (t) => {
  const dir = fixture(t, 'x173-vitest-');
  symlinkSync(path.join(root, 'node_modules'), path.join(dir, 'node_modules'), 'dir');
  writeFileSync(path.join(dir, 'fixture.test.js'), `
import { test, expect } from 'vitest';
test('passes', () => expect(2).toBe(2));
test('reports assertion', () => expect(2).toBe(3));
`);
  const output = path.join(dir, 'vitest-report.json');
  const result = run(path.join(root, 'node_modules/.bin/vitest'), ['run', '--root', dir, '--maxWorkers=1', '--reporter=json', `--outputFile=${output}`], { cwd: root, timeout: 30_000 });
  assert.equal(result.status, 1, result.stderr);
  assert.ok(existsSync(output), `Vitest did not write JSON output. stdout: ${result.stdout}\nstderr: ${result.stderr}`);
  const report = JSON.parse(readFileSync(output, 'utf8'));
  const failures = vitestFailures(report, '@kb-labs/fixture');
  assert.equal(failures.length, 1);
  assert.match(failures[0].name, /@kb-labs\/fixture.*reports assertion/);
  assert.match(failures[0].message, /expected 2 to be 3/i);
  assert.deepEqual(vitestFailures({ testResults: report.testResults.filter((item) => item.status === 'passed') }, '@kb-labs/fixture'), []);
});

// case: S-262.6
test('decodes reports emitted by kb-devkit check --json', (t) => {
  const dir = fixture(t, 'x173-devkit-');
  mkdirSync(path.join(dir, 'packages', 'pass'), { recursive: true });
  mkdirSync(path.join(dir, 'packages', 'fail'), { recursive: true });
  writeFileSync(path.join(dir, 'package.json'), JSON.stringify({ name: 'fixture-workspace', private: true }));
  writeFileSync(path.join(dir, 'pnpm-workspace.yaml'), 'packages:\n  - packages/*\n');
  writeFileSync(path.join(dir, 'devkit.yaml'), `
schemaVersion: 2
workspace:
  packageManager: pnpm
  categories:
    fixtures:
      match: ["packages/*"]
      preset: fixture
presets:
  fixture:
    language: typescript
    package_json:
      required_fields: [name, version]
`);
  writeFileSync(path.join(dir, 'packages/pass/package.json'), JSON.stringify({ name: '@fixture/pass', version: '1.0.0' }));
  writeFileSync(path.join(dir, 'packages/fail/package.json'), JSON.stringify({ name: '@fixture/fail' }));
  const cli = path.join(root, 'tools/kb-devkit/kb-devkit');
  const result = run(cli, ['--config', path.join(dir, 'devkit.yaml'), 'check', '--json'], { cwd: dir });
  assert.equal(result.status, 1, result.stdout);
  const report = JSON.parse(result.stdout);
  const failures = checkFailures(report);
  assert.ok(failures.length > 0);
  assert.ok(failures.some((failure) => failure.name.includes('@fixture/fail')));
  assert.ok(!failures.some((failure) => failure.name.includes('@fixture/pass')));
  assert.deepEqual(checkFailures({ data: { results: [] } }), []);
});

// case: S-262.6
test('decodes check execution failures with their check and package from kb-devkit JSON', (t) => {
  const dir = fixture(t, 'x173-devkit-check-failure-');
  mkdirSync(path.join(dir, 'packages', 'fail'), { recursive: true });
  writeFileSync(path.join(dir, 'package.json'), JSON.stringify({ name: 'fixture-workspace', private: true }));
  writeFileSync(path.join(dir, 'pnpm-workspace.yaml'), 'packages:\n  - packages/*\n');
  writeFileSync(path.join(dir, 'devkit.yaml'), `
schemaVersion: 2
workspace:
  packageManager: pnpm
  categories:
    fixtures:
      match: ["packages/*"]
      preset: fixture
presets:
  fixture:
    language: typescript
custom_checks:
  - name: fixture-check
    run: 'echo fixture check execution failure >&2; exit 23'
    categories: [fixtures]
`);
  writeFileSync(path.join(dir, 'packages/fail/package.json'), JSON.stringify({ name: '@fixture/check-fail', version: '1.0.0' }));
  const result = run(path.join(root, 'tools/kb-devkit/kb-devkit'), ['--config', path.join(dir, 'devkit.yaml'), 'check', '--json'], { cwd: dir });
  assert.equal(result.status, 1, result.stdout);
  const report = JSON.parse(result.stdout);
  const failures = checkFailures(report);
  assert.ok(failures.length > 0, JSON.stringify(report));
  assert.ok(failures.some(({ name, message }) => name.includes('fixture-check') && name.includes('@fixture/check-fail') && message.includes('status 23')));
});

// case: S-262.6
test('decodes failed build package, task, and output tail from kb-devkit run --json', (t) => {
  const dir = fixture(t, 'x173-devkit-build-failure-');
  mkdirSync(path.join(dir, 'packages', 'fail'), { recursive: true });
  writeFileSync(path.join(dir, 'package.json'), JSON.stringify({ name: 'fixture-workspace', private: true }));
  writeFileSync(path.join(dir, 'pnpm-workspace.yaml'), 'packages:\n  - packages/*\n');
  writeFileSync(path.join(dir, 'devkit.yaml'), `
schemaVersion: 2
workspace:
  packageManager: pnpm
  categories:
    fixtures:
      match: ["packages/*"]
      preset: fixture
presets:
  fixture:
    language: typescript
tasks:
  build:
    - categories: [fixtures]
      command: "sh -c 'echo fixture build output tail >&2; exit 17'"
`);
  writeFileSync(path.join(dir, 'packages/fail/package.json'), JSON.stringify({ name: '@fixture/build-fail', version: '1.0.0' }));
  const result = run(path.join(root, 'tools/kb-devkit/kb-devkit'), ['--config', path.join(dir, 'devkit.yaml'), 'run', 'build', '--json'], { cwd: dir });
  assert.equal(result.status, 1, result.stdout);
  const report = JSON.parse(result.stdout);
  assert.equal(typeof stageResults.runFailures, 'function', 'runFailures must decode kb-devkit run --json output');
  const failures = stageAdapters['full-build'].failures({ stdout: result.stdout });
  assert.ok(failures.length > 0, JSON.stringify(report));
  assert.ok(failures.some(({ name, message }) => name.includes('@fixture/build-fail') && name.includes('build') && message.includes('fixture build output tail')));
});

// case: S-262.6
test('an adapter exception becomes a named stage failure and the full verdict records later stages', async () => {
  const executed = [];
  const jobs = await runProfileStages({
    stageNames: ['full-build', 'kb-devkit-check'],
    adapters: {
      'full-build': {
        command: ['kb-devkit', 'run', 'build', '--json'],
        failures: () => { throw new Error('injected decoder exception'); },
      },
      'kb-devkit-check': {
        command: ['kb-devkit', 'check', '--json'],
        failures: () => [],
      },
    },
    execute: async (name) => {
      executed.push(name);
      return { code: name === 'full-build' ? 1 : 0, stdout: '', stderr: '' };
    },
  });
  const startedAtMs = 1_000;
  const verdict = parseVerdict({
    schemaVersion: 1,
    sha: 'a'.repeat(40),
    status: 'fail',
    jobs,
    cause: {
      operation: jobs[0].name,
      command: jobs[0].command,
      attempts: 1,
      startedAtMs,
      endedAtMs: startedAtMs + 1,
      elapsedMs: 1,
      lastError: jobs[0].failures[0].message,
    },
  }, 'a'.repeat(40));
  assert.deepEqual(executed, ['full-build', 'kb-devkit-check']);
  assert.deepEqual(verdict.jobs.map(({ name, status }) => [name, status]), [
    ['full-build', 'fail'],
    ['kb-devkit-check', 'pass'],
  ]);
  assert.equal(verdict.jobs[0].failures[0].name, 'full-build result adapter');
  assert.match(verdict.jobs[0].failures[0].message, /injected decoder exception/);
});

// case: S-262.6
test('decodes events emitted by go test -json', (t) => {
  const dir = fixture(t, 'x173-go-');
  writeFileSync(path.join(dir, 'go.mod'), 'module x173fixture\n\ngo 1.26.0\n');
  writeFileSync(path.join(dir, 'fixture_test.go'), `package x173fixture
import "testing"
func TestPass(t *testing.T) {}
func TestFailure(t *testing.T) { t.Fatal("fixture assertion failed") }
`);
  const result = run('go', ['test', '-json', './...'], { cwd: dir });
  assert.equal(result.status, 1, result.stderr);
  const failures = goTestFailures(result.stdout);
  assert.deepEqual(failures.map(({ name }) => name), ['x173fixture › TestFailure']);
  assert.match(failures[0].message, /fixture assertion failed/);
  assert.deepEqual(goTestFailures(result.stdout.split('\n').filter((line) => !line.includes('"Test":"TestFailure"')).join('\n')), []);
});

// case: S-262.6
test('decodes test ids and tracebacks collected by the unittest runner', (t) => {
  const dir = fixture(t, 'x173-unittest-');
  writeFileSync(path.join(dir, 'test_fixture.py'), `import unittest
class FixtureTests(unittest.TestCase):
    def test_passes(self):
        print("runner stdout noise")
        self.assertEqual(1, 1)
    def test_failure(self):
        self.assertEqual(1, 2, "fixture assertion failed")
`);
  const runner = path.join(root, 'scripts/ci/run-unittest-suite.py');
  const result = run('python3', [runner, '--start-directory', dir], { cwd: root });
  assert.equal(result.status, 1, result.stderr);
  const failures = journeyFailures(JSON.parse(result.stdout));
  assert.deepEqual(failures.map(({ name }) => name), ['test_fixture.FixtureTests.test_failure']);
  assert.match(failures[0].message, /fixture assertion failed/);
  assert.deepEqual(journeyFailures({ failures: [], errors: [] }), []);
});

// case: S-262.6
test('bounds each failure message and total serialized diagnostics', () => {
  const failures = Array.from({ length: 100 }, (_, index) => ({ name: `test-${index}`, message: 'x'.repeat(20_000) }));
  const bounded = boundFailureUnits(failures);
  assert.ok(bounded.length > 0);
  assert.ok(bounded.length < failures.length);
  assert.ok(bounded.every((failure) => failure.message.length <= 4_000));
  assert.ok(Buffer.byteLength(JSON.stringify(bounded)) <= 8_000);
});

test('rejects nonzero stage results when the adapter names no failure unit', () => {
  assert.equal(typeof stageResults.requireFailureUnits, 'function', 'the adapter must enforce named failures');
  assert.throws(() => stageResults.requireFailureUnits('full-build', []), /nonzero full-build stage produced no named failure unit/);
});

// case: S-262.6
test('the structured adapters cover exactly the stages selected by the verification profile', () => {
  const profile = JSON.parse(readFileSync(path.join(root, 'scripts/ci/verification-profile.json'), 'utf8'));
  assert.doesNotThrow(() => assertStageAdaptersMatchProfile(profile.stages));
  assert.ok(profile.stages.every((name) => Array.isArray(stageAdapters[name].command) && typeof stageAdapters[name].failures === 'function'));
  assert.throws(() => assertStageAdaptersMatchProfile(profile.stages.slice(1)), /must match exactly/);
});
