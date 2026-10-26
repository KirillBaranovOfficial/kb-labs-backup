export function failingNames(log) {
  return log.split(/\r?\n/).filter((line) => /(^FAIL\s|^--- FAIL:|Tests\s+\d+ failed|ERR_PNPM|^✖\s|^FAILED\s)/.test(line)).slice(-40);
}

export function boundedTail(log) {
  return log.split(/\r?\n/).slice(-80).join('\n').slice(-12_000);
}

const PROFILE_STOP_POLICY = new Map([
  ['stream', true],
  ['candidate', false],
]);

export async function runProfile({ profile, stages, runCommand, now = Date.now }) {
  const stopAfterFailure = PROFILE_STOP_POLICY.get(profile);
  if (stopAfterFailure === undefined) throw new Error(`Unknown verification profile: ${profile}`);
  const jobs = [];
  for (const stage of stages) {
    const startedAt = now();
    const result = await runCommand(stage.command, stage.args);
    const finishedAt = now();
    const log = [result.stdout, result.stderr, result.error?.message].filter(Boolean).join('\n');
    jobs.push({
      name: stage.name,
      status: result.status === 0 ? 'pass' : 'fail',
      failures: failingNames(log),
      logTail: boundedTail(log),
      startedAt: new Date(startedAt).toISOString(),
      finishedAt: new Date(finishedAt).toISOString(),
      durationMs: Math.max(0, finishedAt - startedAt),
    });
    stage.onComplete?.(jobs.at(-1));
    if (stopAfterFailure && jobs.at(-1).status === 'fail') break;
  }
  return jobs;
}
