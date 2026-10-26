#!/usr/bin/env node
import { appendFileSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { parseVerdict } from './remote-verdict.mjs';
import { runProfile } from './remote-profile.mjs';

const local = process.argv[2] === '--local';
const expectedSha = process.argv[local ? 3 : 2];
const profileIndex = process.argv.indexOf('--profile');
const profileName = profileIndex < 0 ? 'stream' : process.argv[profileIndex + 1];
if (!/^[0-9a-f]{40}$/.test(expectedSha ?? '')) throw new Error('A full commit SHA is required');
if (!profileName) throw new Error('The --profile option requires a profile name');
const profile = JSON.parse(readFileSync(new URL('./verification-profile.json', import.meta.url), 'utf8'));
const outputDir = local ? 'reports' : '.';
const outputName = local ? `local-verification-${expectedSha}.json` : 'verdict.json';
const logFile = process.env.GITHUB_STEP_SUMMARY ? `${process.env.RUNNER_TEMP}/remote-verification.log` : `reports/local-verification-${expectedSha}.log`;
const commands = {
  'workspace-build': (name) => ['./tools/kb-devkit/kb-devkit', 'run', 'build', ...(name === 'stream' ? ['--affected'] : [])],
  'workspace-unit-tests': (name) => ['corepack', 'pnpm', '-r', ...(name === 'candidate' ? ['--no-bail'] : []), '--filter', ...(name === 'candidate' ? [] : [`...[origin/${process.env.DEFAULT_BRANCH ?? 'master'}]`]), '--filter', '!@kb-labs/monorepo', '--filter', '!@kb-labs/e2e', '--filter', '@kb-labs/cli-bin', '--workspace-concurrency=2', 'test'],
  'kb-devkit-check': () => ['./tools/kb-devkit/kb-devkit', 'check'],
  'kb-create-go-suite': () => ['node', 'scripts/ci/go-module-suite.mjs', '--module', 'tools/kb-create'],
  'journey-unit-suite': () => ['python3', '-m', 'unittest', 'discover', '-s', 'e2e/journey', '-p', 'test_*.py'],
};

let verifiedSha;
if (local) mkdirSync(outputDir, { recursive: true });
const checkout = spawnSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' });
if (checkout.status !== 0) throw new Error(checkout.stderr.trim());
verifiedSha = checkout.stdout.trim();
if (verifiedSha !== expectedSha) throw new Error(`Checkout SHA mismatch: expected ${expectedSha}, received ${verifiedSha}`);

const stages = profile.stages.map((name) => {
  const commandFactory = commands[name];
  if (!commandFactory) throw new Error(`No command is registered for profile stage ${name}`);
  const [command, ...args] = commandFactory(profileName);
  return {
    name,
    command,
    args,
    onComplete: (stage) => {
      const log = `${stage.name}: ${stage.status} (${stage.durationMs} ms)`;
      appendFileSync(logFile, `\n## ${stage.name}\n${stage.logTail}\n`);
      if (process.env.GITHUB_STEP_SUMMARY) appendFileSync(process.env.GITHUB_STEP_SUMMARY, `- ${log}\n`);
    },
  };
});
const jobs = await runProfile({
  profile: profileName,
  stages,
  runCommand: (command, args) => spawnSync(command, args, { encoding: 'utf8', env: process.env, maxBuffer: 16 * 1024 * 1024 }),
});
const verdict = parseVerdict({ schemaVersion: 1, sha: verifiedSha, status: jobs.every((stage) => stage.status === 'pass') ? 'pass' : 'fail', jobs }, expectedSha);
writeFileSync(`${outputDir}/${outputName}`, `${JSON.stringify(verdict, null, 2)}\n`);
if (process.env.GITHUB_STEP_SUMMARY) appendFileSync(process.env.GITHUB_STEP_SUMMARY, `\nRemote verification: **${verdict.status}** for \`${verifiedSha}\`.\n`);
if (local) process.stdout.write(`Local verification ${verdict.status} for ${verifiedSha}; details: ${outputDir}/${outputName} and ${logFile}\n`);
process.exitCode = verdict.status === 'pass' ? 0 : 1;
