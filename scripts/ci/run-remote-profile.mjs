#!/usr/bin/env node
import { appendFileSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { spawn, spawnSync } from 'node:child_process';
import { performance } from 'node:perf_hooks';
import { parseVerdict } from './remote-verdict.mjs';
import { boundFailureUnits, checkFailures, failedWithoutUnit, journeyFailures } from './remote-stage-results.mjs';
import { interruptActiveStage } from './remote-profile-signals.mjs';
import { stageFinishedLine, stageStartedLine } from './remote-profile-progress.mjs';

const local = process.argv[2] === '--local';
const expectedSha = process.argv[local ? 3 : 2];
if (!/^[0-9a-f]{40}$/.test(expectedSha ?? '')) throw new Error('A full commit SHA is required');
const profile = JSON.parse(readFileSync(new URL('./verification-profile.json', import.meta.url), 'utf8'));
const outputDir = local ? 'reports' : '.';
const outputName = local ? `local-verification-${expectedSha}.json` : 'verdict.json';
const verdictPath = `${outputDir}/${outputName}`;
const logFile = process.env.GITHUB_STEP_SUMMARY ? `${process.env.RUNNER_TEMP}/remote-verification.log` : `reports/local-verification-${expectedSha}.log`;
const stages = [];
const commands = {
  'full-build': ['./tools/kb-devkit/kb-devkit', 'run', 'build'],
  'affected-unit-tests': ['node', 'scripts/ci/run-affected-unit-tests.mjs'],
  'kb-devkit-check': ['./tools/kb-devkit/kb-devkit', 'check', '--json'],
  'kb-create-go-suite': ['node', 'scripts/ci/go-module-suite.mjs', '--json', '--module', 'tools/kb-create'],
  'journey-unit-suite': ['python3', 'scripts/ci/run-unittest-suite.py'],
};
let verifiedSha;
let active;
let receivedSignal;

function stageFailures(name, result) {
  if (result.code === 0) return [];
  try {
    if (name === 'kb-devkit-check') return checkFailures(JSON.parse(result.stdout));
    if (name === 'kb-create-go-suite') return boundFailureUnits(JSON.parse(result.stdout).failures ?? []);
    if (name === 'journey-unit-suite') return journeyFailures(JSON.parse(result.stdout));
    if (name === 'affected-unit-tests') return boundFailureUnits(JSON.parse(result.stdout).failures ?? []);
  } catch {
    return failedWithoutUnit(name);
  }
  return failedWithoutUnit(name);
}

function saveVerdict() {
  const verdict = parseVerdict({ schemaVersion: 1, sha: verifiedSha, status: stages.every((stage) => stage.status === 'pass') ? 'pass' : 'fail', jobs: stages }, expectedSha);
  writeFileSync(verdictPath, `${JSON.stringify(verdict, null, 2)}\n`);
  return verdict;
}

function onSignal(signal) {
  if (receivedSignal) return;
  receivedSignal = signal;
  const stage = interruptActiveStage(signal, active, stages, saveVerdict, performance.now());
  if (stage) process.stderr.write(stageFinishedLine(stage.name, stage));
}

process.on('SIGTERM', () => onSignal('SIGTERM'));
process.on('SIGINT', () => onSignal('SIGINT'));

if (local) mkdirSync(outputDir, { recursive: true });
const checkout = spawnSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' });
if (checkout.status !== 0) throw new Error(checkout.stderr.trim());
verifiedSha = checkout.stdout.trim();
if (verifiedSha !== expectedSha) throw new Error(`Checkout SHA mismatch: expected ${expectedSha}, received ${verifiedSha}`);

for (const name of profile.stages) {
  if (receivedSignal) break;
  const [command, ...args] = commands[name] ?? [];
  if (!command) throw new Error(`No command is registered for profile stage ${name}`);
  const startedAt = performance.now();
  process.stdout.write(stageStartedLine(name, [command, ...args].join(' ')));
  const result = await runStage(name, command, args, process.env, startedAt);
  const durationMs = Math.round(performance.now() - startedAt);
  const log = [result.stdout, result.stderr].filter(Boolean).join('\n');
  appendFileSync(logFile, `\n## ${name}\n${log}\n`);
  const recordedBySignal = active?.recorded ?? false;
  if (!active?.recorded) {
    const failures = stageFailures(name, result);
    if (result.code !== 0 && failures.length === 0) failures.push(...failedWithoutUnit(name));
    if (result.signal) failures.unshift({ name: `${name} signal`, message: `${name} terminated by signal ${result.signal}` });
    stages.push({
      name,
      status: result.code === 0 ? 'pass' : 'fail',
      failures: boundFailureUnits(failures),
      logTail: boundFailureUnits(failures).map((failure) => `${failure.name}\n${failure.message}`).join('\n\n').slice(0, 1_000),
      exitCode: result.code,
      signal: result.signal ?? null,
      durationMs,
    });
  }
  active = undefined;
  const stage = stages.at(-1);
  if (stage?.name === name) {
    if (stage.durationMs !== durationMs && !stage.signal) stage.durationMs = durationMs;
    if (!recordedBySignal) process.stdout.write(stageFinishedLine(name, stage));
    saveVerdict();
  }
  if (receivedSignal) break;
}

const verdict = saveVerdict();
if (process.env.GITHUB_STEP_SUMMARY) appendFileSync(process.env.GITHUB_STEP_SUMMARY, `\nRemote verification: **${verdict.status}** for \`${verifiedSha}\`.\n`);
if (local) process.stdout.write(`Local verification ${verdict.status} for ${verifiedSha}; details: ${verdictPath} and ${logFile}\n`);
process.exitCode = receivedSignal ? (receivedSignal === 'SIGTERM' ? 143 : 130) : verdict.status === 'pass' ? 0 : 1;

function runStage(name, command, args, env, startedAt) {
  return new Promise((resolve) => {
    const child = spawn(command, args, { env, detached: process.platform !== 'win32', stdio: ['ignore', 'pipe', 'pipe'] });
    active = { child, name, startedAt, recorded: false };
    let stdout = '';
    let stderr = '';
    child.stdout.setEncoding('utf8').on('data', (chunk) => { stdout += chunk; });
    child.stderr.setEncoding('utf8').on('data', (chunk) => { stderr += chunk; });
    child.once('error', (error) => resolve({ code: 127, stdout, stderr: `${stderr}\n${error.message}` }));
    child.once('close', (code, signal) => resolve({ code: signal ? 128 + ({ SIGTERM: 15, SIGINT: 2 }[signal] ?? 1) : code, signal, stdout, stderr }));
  });
}
