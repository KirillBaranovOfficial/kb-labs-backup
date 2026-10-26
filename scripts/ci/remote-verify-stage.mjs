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
    args = ['run', 'build', '--packages', selected.join(','), '--json'];
  } else if (stage === profile.packageTestStage) {
    if (!Array.isArray(packageNames) || packageNames.length === 0 || packageNames.some((packageName) => typeof packageName !== 'string' || !packageName)) throw new Error(`${profile.packageTestStage} requires packages selected by kb-devkit affected --json.`);
    if (!Number.isSafeInteger(shard) || shard < 1) throw new Error(`${profile.packageTestStage} requires a positive shard number from the remote verification plan.`);
    command = './tools/kb-devkit/kb-devkit';
    args = ['run', 'test', '--packages', packageNames.join(','), '--concurrency', '1', '--json'];
    name = `${stage}:shard-${shard}`;
  } else {
    [command, args] = commandFor(definition);
  }
  const startedAt = now();
  const result = await runCommand(command, args, { cwd: root });
  const finishedAt = now();
  const log = [result.stdout, result.stderr, result.error?.message].filter(Boolean).join('\n');
  const taskFailures = devkitTaskFailures(stage, result);
  const taskLogOutput = taskFailures.map((failure) => {
    const label = [failure.package, failure.task].filter(Boolean).join(':');
    return [label, failure.logPath, failure.logTail || failure.error].filter(Boolean).join('\n');
  }).join('\n\n');
  const isDevkitStage = stage === profile.buildStage || stage === profile.packageTestStage;
  const visibleLog = taskFailures.length ? taskLogOutput : isDevkitStage ? (result.stderr || '') : log;
  return {
    name,
    status: result.status === 0 ? 'pass' : 'fail',
    selectedPackageCount: stage === profile.packageTestStage ? packageNames.length : selectedPackageCount,
    failures: [...failingNames(log), ...taskFailures.flatMap((failure) => (failure.logTail || failure.error || '').split(/\r?\n/).filter(Boolean))],
    taskFailures,
    logTail: boundedTail(visibleLog),
    startedAt: new Date(startedAt).toISOString(),
    finishedAt: new Date(finishedAt).toISOString(),
    durationMs: Math.max(0, finishedAt - startedAt),
    ...(stage === profile.packageTestStage ? { packages: packageNames } : {}),
    cache: cacheRecord(cache),
  };
}

function devkitTaskFailures(stage, result) {
  if ((stage !== profile.buildStage && stage !== profile.packageTestStage) || result.status === 0) return [];
  let envelope;
  try {
    envelope = JSON.parse(result.stdout);
  } catch (error) {
    return [{ error: `kb-devkit run --json returned invalid JSON: ${error.message}` }];
  }
  const results = envelope?.data?.results;
  if (envelope?.ok !== false || !Array.isArray(results)) {
    return [{ error: 'kb-devkit run --json returned no structured task results for a failed stage.' }];
  }
  return results.filter((task) => task?.OK === false).map((task) => {
    const failure = { package: task.Package, task: task.Task, logPath: task.logPath };
    if (typeof task.logPath !== 'string' || task.logPath.length === 0) {
      return { ...failure, error: 'failed task result did not include its structured logPath.' };
    }
    try {
      failure.logTail = boundedTail(readFileSync(task.logPath, 'utf8'));
    } catch (error) {
      failure.error = `could not read task log: ${error.message}`;
    }
    return failure;
  });
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
