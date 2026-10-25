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
  failedWithoutUnit,
  goTestFailures,
  journeyFailures,
  vitestFailures,
} from './remote-stage-results.mjs';

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
  assert.deepEqual(checkFailures({ results: [] }), []);
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

test('names a failed stage when structured output contains no failure units', () => {
  assert.deepEqual(failedWithoutUnit('kb-devkit-check'), [{
    name: 'kb-devkit-check: stage failed without naming a failure',
    message: 'stage failed without naming a failure',
  }]);
});
