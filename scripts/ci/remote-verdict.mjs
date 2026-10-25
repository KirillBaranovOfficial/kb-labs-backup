export function parseVerdict(value, expectedSha) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('Remote verdict must be a JSON object');
  if (value.sha !== expectedSha) throw new Error(`Remote verdict SHA mismatch: expected ${expectedSha}, received ${String(value.sha)}`);
  if (!['pass', 'fail', 'timeout', 'outage'].includes(value.status)) throw new Error(`Invalid remote verdict status: ${String(value.status)}`);
  if (value.status !== 'pass') validateCause(value.cause);
  if (!Array.isArray(value.jobs)) throw new Error('Remote verdict jobs must be an array');
  for (const job of value.jobs) {
    if (!job || typeof job.name !== 'string' || !['pass', 'fail', 'timeout', 'outage', 'skipped'].includes(job.status)) {
      throw new Error('Remote verdict contains an invalid job result');
    }
    if (!Array.isArray(job.failures) || typeof job.logTail !== 'string') throw new Error(`Remote verdict job ${job.name} is missing diagnostics`);
    if (job.failures.some((failure) => !failure || typeof failure.name !== 'string' || !failure.name || typeof failure.message !== 'string' || !failure.message)) {
      throw new Error(`Remote verdict job ${job.name} contains an unnamed failure`);
    }
    if (job.status === 'fail' && job.failures.length === 0) throw new Error(`Remote verdict job ${job.name} failed without naming a failure`);
  }
  if (value.status === 'pass' && value.jobs.some((job) => job.status !== 'pass')) throw new Error('A passing verdict cannot contain a non-passing job');
  if (Buffer.byteLength(JSON.stringify(value)) > 64_000) throw new Error('Remote verdict diagnostics exceed the 64 KB limit');
  return value;
}

export function validateCause(cause) {
  if (!cause || typeof cause !== 'object' || Array.isArray(cause)) throw new Error('Remote verdict is missing its structured cause');
  if (typeof cause.operation !== 'string' || !cause.operation || typeof cause.command !== 'string' || !cause.command) throw new Error('Remote verdict cause must name its operation and command');
  if (!Number.isInteger(cause.attempts) || cause.attempts < 0) throw new Error('Remote verdict cause attempts must be a non-negative integer');
  for (const field of ['startedAtMs', 'endedAtMs', 'elapsedMs']) {
    if (!Number.isFinite(cause[field]) || cause[field] < 0) throw new Error(`Remote verdict cause ${field} must be a non-negative number`);
  }
  if (cause.endedAtMs < cause.startedAtMs || cause.elapsedMs !== cause.endedAtMs - cause.startedAtMs) throw new Error('Remote verdict cause time span is inconsistent');
  if (typeof cause.lastError !== 'string' || !cause.lastError) throw new Error('Remote verdict cause must include its last error');
  if (cause.operation.length > 1_000 || cause.command.length > 1_000 || cause.lastError.length > 1_000) throw new Error('Remote verdict cause exceeds its 1,000 character field limit');
  return cause;
}

export function terminalVerdict(sha, status, cause, message) {
  if (!['fail', 'timeout', 'outage'].includes(status)) throw new Error(`Invalid terminal verdict status: ${status}`);
  validateCause(cause);
  return { schemaVersion: 1, sha, status, jobs: [], diagnostics: [String(message).slice(0, 1_000)], cause };
}

export function repositoryFromRemote(url) {
  const match = url.match(/(?:^[^@]+@[^:]+:|^https?:\/\/[^/]+\/)([^/]+\/[^/.]+?)(?:\.git)?$/);
  if (!match) throw new Error(`backup remote is not a GitHub repository: ${url}`);
  return match[1];
}

export function selectVerificationRun(runs, { branch, sha, workflowPath }) {
  return runs.find((item) => item.event === 'push' && item.head_branch === branch && item.head_sha === sha && item.path === workflowPath);
}
