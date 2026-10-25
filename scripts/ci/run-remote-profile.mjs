#!/usr/bin/env node
import { appendFileSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { parseVerdict } from './remote-verdict.mjs';

const local = process.argv[2] === '--local';
const expectedSha = process.argv[local ? 3 : 2];
if (!/^[0-9a-f]{40}$/.test(expectedSha ?? '')) throw new Error('A full commit SHA is required');
const profile = JSON.parse(readFileSync(new URL('./verification-profile.json', import.meta.url), 'utf8'));
const outputDir = local ? 'reports' : '.';
const outputName = local ? `local-verification-${expectedSha}.json` : 'verdict.json';
const logFile = process.env.GITHUB_STEP_SUMMARY ? `${process.env.RUNNER_TEMP}/remote-verification.log` : `reports/local-verification-${expectedSha}.log`;
const stages = [];
const commands = {
  'full-build': ['./tools/kb-devkit/kb-devkit', 'run', 'build'],
  'affected-unit-tests': ['corepack', 'pnpm', '-r', '--no-bail', '--filter', `...[origin/${process.env.DEFAULT_BRANCH ?? 'master'}]`, '--filter', '!@kb-labs/monorepo', '--filter', '!@kb-labs/e2e', '--filter', '@kb-labs/cli-bin', '--workspace-concurrency=2', 'test'],
  'kb-devkit-check': ['./tools/kb-devkit/kb-devkit', 'check'],
  'kb-create-go-suite': ['node', 'scripts/ci/go-module-suite.mjs', '--module', 'tools/kb-create'],
  'journey-unit-suite': ['python3', '-m', 'unittest', 'discover', '-s', 'e2e/journey', '-p', 'test_*.py'],
};

function failingNames(log) {
  return log.split(/\r?\n/).filter((line) => /(^FAIL\s|^--- FAIL:|Tests\s+\d+ failed|ERR_PNPM|^✖\s|^FAILED\s)/.test(line)).slice(-40);
}

function boundedTail(log) {
  return log.split(/\r?\n/).slice(-80).join('\n').slice(-12_000);
}

let verifiedSha;
if (local) mkdirSync(outputDir, { recursive: true });
const checkout = spawnSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' });
if (checkout.status !== 0) throw new Error(checkout.stderr.trim());
verifiedSha = checkout.stdout.trim();
if (verifiedSha !== expectedSha) throw new Error(`Checkout SHA mismatch: expected ${expectedSha}, received ${verifiedSha}`);

for (const name of profile.stages) {
  const [command, ...args] = commands[name] ?? [];
  if (!command) throw new Error(`No command is registered for profile stage ${name}`);
  const result = spawnSync(command, args, { encoding: 'utf8', env: process.env, maxBuffer: 16 * 1024 * 1024 });
  const log = [result.stdout, result.stderr, result.error?.message].filter(Boolean).join('\n');
  appendFileSync(logFile, `\n## ${name}\n${log}\n`);
  stages.push({ name, status: result.status === 0 ? 'pass' : 'fail', failures: failingNames(log), logTail: boundedTail(log) });
}

const verdict = parseVerdict({ schemaVersion: 1, sha: verifiedSha, status: stages.every((stage) => stage.status === 'pass') ? 'pass' : 'fail', jobs: stages }, expectedSha);
writeFileSync(`${outputDir}/${outputName}`, `${JSON.stringify(verdict, null, 2)}\n`);
if (process.env.GITHUB_STEP_SUMMARY) appendFileSync(process.env.GITHUB_STEP_SUMMARY, `\nRemote verification: **${verdict.status}** for \`${verifiedSha}\`.\n`);
if (local) process.stdout.write(`Local verification ${verdict.status} for ${verifiedSha}; details: ${outputDir}/${outputName} and ${logFile}\n`);
process.exitCode = verdict.status === 'pass' ? 0 : 1;
