import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { execFileSync, spawnSync } from 'node:child_process';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { runStage } from './remote-verify-stage.mjs';

const profile = JSON.parse(readFileSync(new URL('./verification-profile.json', import.meta.url), 'utf8'));
const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
// case: S-262.7
test('workspace package shard uses kb-devkit and records a stage result with elapsed time', async () => {
  let now = 1_000;
  let invocation;
  const result = await runStage({
    stage: profile.packageTestStage,
    packageNames: ['@kb-labs/core', '@kb-labs/cli'],
    selectedPackageCount: 41,
    shard: 3,
    runCommand: async (command, args) => {
      invocation = [command, args];
      now += 250;
      return { status: 0, stdout: 'passed', stderr: '' };
    },
    now: () => now,
  });

  assert.deepEqual(invocation, ['./tools/kb-devkit/kb-devkit', ['run', 'test', '--packages', '@kb-labs/core,@kb-labs/cli', '--concurrency', '1', '--json']]);
  assert.equal(result.name, `${profile.packageTestStage}:shard-3`);
  assert.deepEqual(result.packages, ['@kb-labs/core', '@kb-labs/cli']);
  assert.equal(result.status, 'pass');
  assert.equal(result.durationMs, 250);
  assert.equal(result.selectedPackageCount, 2);
});
// case: S-262.7
test('journey unit shard records its failure instead of losing it before artifact upload', async () => {
  const result = await runStage({
    stage: profile.buildDependentTestStages.candidate[0],
    selectedPackageCount: 41,
    runCommand: async () => ({ status: 1, stdout: '', stderr: 'test failure' }),
    now: (() => { let value = 0; return () => value += 10; })(),
  });

  assert.equal(result.status, 'fail');
  assert.match(result.logTail, /test failure/);
  assert.equal(result.durationMs, 10);
  assert.equal(result.selectedPackageCount, 41);
});

// case: S-262.7
test('failed kb-devkit task contributes the task log tail identified by structured JSON', async () => {
  const temporaryRoot = mkdtempSync(path.join(os.tmpdir(), 'remote-stage-task-log-'));
  const fixtureRoot = path.join(temporaryRoot, 'workspace');
  const packageRoot = path.join(fixtureRoot, 'packages/site');
  const configPath = path.join(fixtureRoot, 'devkit.yaml');
  const binary = path.join(temporaryRoot, 'kb-devkit');
  const home = path.join(temporaryRoot, 'home');
  try {
    mkdirSync(packageRoot, { recursive: true });
    writeFileSync(path.join(fixtureRoot, 'package.json'), JSON.stringify({ workspaces: ['packages/*'] }));
    writeFileSync(path.join(packageRoot, 'package.json'), JSON.stringify({ name: '@fixture/site' }));
    writeFileSync(configPath, [
      'workspace:',
      '  categories:',
      '    site:',
      '      match: ["packages/site"]',
      '      preset: node-lib',
      'tasks:',
      '  build:',
      '    - categories: [site]',
      `      command: 'sh -c "echo ENOENT: no such file or directory, mkdir .next/export; echo Error occurred prerendering page /404; exit 1"'`,
      '',
    ].join('\n'));
    execFileSync('go', ['build', '-o', binary, '.'], { cwd: path.join(repoRoot, 'tools/kb-devkit'), stdio: 'pipe' });

    let invocation;
    let nodeOptions;
    const result = await runStage({
      stage: profile.buildStage,
      packages: ['@fixture/site'],
      selectedPackageCount: 1,
      buildLimits: { cpuCount: 2, memoryBudgetBytes: 1_000_000, concurrency: 2, nodeHeapMb: 512 },
      runCommand: async (command, args, options) => {
        invocation = [command, args];
        nodeOptions = options.env.NODE_OPTIONS;
        return spawnSync(binary, ['--config', configPath, ...args], {
          cwd: fixtureRoot,
          encoding: 'utf8',
          env: { ...process.env, ...options.env, HOME: home },
        });
      },
      now: (() => { let value = 0; return () => value += 10; })(),
    });

    assert.deepEqual(invocation, ['./tools/kb-devkit/kb-devkit', ['run', 'build', '--packages', '@fixture/site', '--concurrency', '2', '--json']]);
    assert.equal(nodeOptions, '--max-old-space-size=512');
    assert.equal(result.status, 'fail');
    assert.ok(result.failures.some((line) => line.includes('ENOENT: no such file or directory, mkdir .next/export')));
    assert.ok(result.taskFailures[0].logTail.includes('Error occurred prerendering page /404'));
    assert.ok(result.logTail.includes('Error occurred prerendering page /404'));
    assert.ok(readFileSync(result.taskFailures[0].logPath, 'utf8').includes('ENOENT: no such file or directory, mkdir .next/export'));
  } finally {
    rmSync(temporaryRoot, { recursive: true, force: true });
  }
});
