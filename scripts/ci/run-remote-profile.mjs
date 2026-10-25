#!/usr/bin/env node
import { appendFileSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { spawn, spawnSync } from 'node:child_process';
import { performance } from 'node:perf_hooks';
import { parseVerdict } from './remote-verdict.mjs';
import { assertStageAdaptersMatchProfile, stageAdapters } from './remote-profile-stage-adapters.mjs';
import { runProfileStages } from './remote-profile-execution.mjs';
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
assertStageAdaptersMatchProfile(profile.stages);
let verifiedSha;
let active;
let receivedSignal;

function saveVerdict() {
  const failedStage = stages.find((stage) => stage.status === 'fail');
  const cause = failedStage ? {
    operation: failedStage.name,
    command: failedStage.command,
    attempts: 1,
    startedAtMs: failedStage.startedAtMs,
    endedAtMs: failedStage.endedAtMs,
    elapsedMs: failedStage.endedAtMs - failedStage.startedAtMs,
    lastError: failedStage.failures[0]?.message.slice(0, 1_000) ?? `${failedStage.name} exited with status ${failedStage.exitCode}`,
  } : undefined;
  const verdict = parseVerdict({ schemaVersion: 1, sha: verifiedSha, status: failedStage ? 'fail' : 'pass', jobs: stages, ...(cause ? { cause } : {}) }, expectedSha);
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

await runProfileStages({
  stageNames: profile.stages,
  adapters: stageAdapters,
  stages,
  shouldStop: () => Boolean(receivedSignal),
  execute: async (name, adapter) => {
    const [command, ...args] = adapter.command;
    const startedAt = performance.now();
    const startedAtMs = Date.now();
    process.stdout.write(stageStartedLine(name, [command, ...args].join(' ')));
    const result = await runStage(name, command, args, process.env, startedAt);
    const durationMs = Math.round(performance.now() - startedAt);
    const log = [result.stdout, result.stderr].filter(Boolean).join('\n');
    appendFileSync(logFile, `\n## ${name}\n${log}\n`);
    return {
      ...result,
      startedAtMs,
      endedAtMs: Date.now(),
      durationMs,
      recordedBySignal: active?.recorded ?? false,
    };
  },
  onStage: (stage, recordedStages, result, recordedBySignal) => {
    active = undefined;
    const recordedStage = recordedBySignal ? recordedStages.at(-1) : stage;
    if (recordedStage?.name === stage.name) {
      if (recordedStage.durationMs !== result.durationMs && !recordedStage.signal) recordedStage.durationMs = result.durationMs;
      if (!recordedBySignal) process.stdout.write(stageFinishedLine(stage.name, recordedStage));
      saveVerdict();
    }
  },
});

const verdict = saveVerdict();
if (process.env.GITHUB_STEP_SUMMARY) appendFileSync(process.env.GITHUB_STEP_SUMMARY, `\nRemote verification: **${verdict.status}** for \`${verifiedSha}\`.\n`);
if (local) process.stdout.write(`Local verification ${verdict.status} for ${verifiedSha}; details: ${verdictPath} and ${logFile}\n`);
process.exitCode = receivedSignal ? (receivedSignal === 'SIGTERM' ? 143 : 130) : verdict.status === 'pass' ? 0 : 1;

function runStage(name, command, args, env, startedAt) {
  return new Promise((resolve) => {
    const child = spawn(command, args, { env, detached: process.platform !== 'win32', stdio: ['ignore', 'pipe', 'pipe'] });
    active = { child, name, command: [command, ...args].join(' '), startedAt, startedAtMs: Date.now(), recorded: false };
    let stdout = '';
    let stderr = '';
    child.stdout.setEncoding('utf8').on('data', (chunk) => { stdout += chunk; });
    child.stderr.setEncoding('utf8').on('data', (chunk) => { stderr += chunk; });
    child.once('error', (error) => resolve({ code: 127, stdout, stderr: `${stderr}\n${error.message}` }));
    child.once('close', (code, signal) => resolve({ code: signal ? 128 + ({ SIGTERM: 15, SIGINT: 2 }[signal] ?? 1) : code, signal, stdout, stderr }));
  });
}
