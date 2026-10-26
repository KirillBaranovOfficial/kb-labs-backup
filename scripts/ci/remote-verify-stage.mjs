import { readFileSync, readdirSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const profile = JSON.parse(readFileSync(new URL('./verification-profile.json', import.meta.url), 'utf8'));
const stageDefinitions = Object.entries(profile.stages).map(([id, definition]) => ({ id, ...definition }));

function failingNames(log) {
  return log.split(/\r?\n/).filter((line) => /(^FAIL\s|^--- FAIL:|Tests\s+\d+ failed|ERR_PNPM|^✖\s|^FAILED\s)/.test(line)).slice(-40);
}

function boundedTail(log) {
  return log.split(/\r?\n/).slice(-80).join('\n').slice(-12_000);
}
function cacheStatus(value) {
  if (value === 'true') return 'exact-hit';
  if (value === 'false') return 'miss-or-restore-prefix';
  return 'unavailable';
}

export async function runStage({ stage, packageNames, packages, selectedPackageCount, shard, runCommand, now = Date.now, cache = {} }) {
  const definition = stageDefinitions.find((entry) => entry.id === stage);
  if (!definition) throw new Error(`Unknown verification stage ${stage}; use the registered stages in scripts/ci/verification-profile.json.`);
  if (!Number.isSafeInteger(selectedPackageCount) || selectedPackageCount < 0) throw new Error(`${stage} requires the selected package count from the remote verification plan.`);
  let command;
  let args;
  let name = stage;
  if (stage === profile.buildStage) {
    name = stage;
    const selected = packages ?? [];
    if (selected.length === 0) {
      const at = now();
      return {
        name, status: 'pass', failures: [], logTail: 'kb-devkit selected no affected workspace packages; build had no work.',
        selectedPackageCount: 0,
        startedAt: new Date(at).toISOString(), finishedAt: new Date(at).toISOString(), durationMs: 0, cache: cacheRecord(cache),
      };
    }
    command = './tools/kb-devkit/kb-devkit';
    args = ['run', 'build', '--packages', selected.join(',')];
  } else if (stage === profile.packageTestStage) {
    if (!Array.isArray(packageNames) || packageNames.length === 0 || packageNames.some((packageName) => typeof packageName !== 'string' || !packageName)) throw new Error(`${profile.packageTestStage} requires packages selected by kb-devkit affected --json.`);
    if (!Number.isSafeInteger(shard) || shard < 1) throw new Error(`${profile.packageTestStage} requires a positive shard number from the remote verification plan.`);
    command = './tools/kb-devkit/kb-devkit';
    args = ['run', 'test', '--packages', packageNames.join(','), '--concurrency', '1'];
    name = `${stage}:shard-${shard}`;
  } else {
    [command, args] = commandFor(definition);
  }
  const startedAt = now();
  const result = await runCommand(command, args, { cwd: root });
  const finishedAt = now();
  const log = [result.stdout, result.stderr, result.error?.message].filter(Boolean).join('\n');
  return {
    name,
    status: result.status === 0 ? 'pass' : 'fail',
    selectedPackageCount: stage === profile.packageTestStage ? packageNames.length : selectedPackageCount,
    failures: failingNames(log),
    logTail: boundedTail(log),
    startedAt: new Date(startedAt).toISOString(),
    finishedAt: new Date(finishedAt).toISOString(),
    durationMs: Math.max(0, finishedAt - startedAt),
    ...(stage === profile.packageTestStage ? { packages: packageNames } : {}),
    cache: cacheRecord(cache),
  };
}

function commandFor(definition) {
  if (definition.runner === 'node-test-directory') {
    const files = readdirSync(path.join(root, definition.directory), { withFileTypes: true })
      .filter((entry) => entry.isFile() && entry.name.endsWith('.test.mjs'))
      .map((entry) => path.join(definition.directory, entry.name))
      .sort();
    return [process.execPath, ['--test', ...files]];
  }
  if (definition.runner === 'node-script') return [process.execPath, [definition.script, ...definition.args]];
  if (definition.runner === 'process' || definition.runner === 'executable') return [definition.command, definition.args];
  throw new Error(`Unsupported verification runner ${definition.runner}; declare a supported runner in scripts/ci/verification-profile.json.`);
}

function cacheRecord(cache) {
  return {
    pnpm: cacheStatus(cache.pnpm),
    go: cacheStatus(cache.go),
    devkit: cacheStatus(cache.devkit),
  };
}
