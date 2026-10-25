export function parseVerdict(value, expectedSha) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('Remote verdict must be a JSON object');
  if (value.sha !== expectedSha) throw new Error(`Remote verdict SHA mismatch: expected ${expectedSha}, received ${String(value.sha)}`);
  if (!['pass', 'fail', 'timeout', 'outage'].includes(value.status)) throw new Error(`Invalid remote verdict status: ${String(value.status)}`);
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

export function terminalVerdict(sha, status, message) {
  if (!['timeout', 'outage'].includes(status)) throw new Error(`Invalid terminal verdict status: ${status}`);
  return { schemaVersion: 1, sha, status, jobs: [], diagnostics: [message] };
}

export function repositoryFromRemote(url) {
  const match = url.match(/(?:^[^@]+@[^:]+:|^https?:\/\/[^/]+\/)([^/]+\/[^/.]+?)(?:\.git)?$/);
  if (!match) throw new Error(`backup remote is not a GitHub repository: ${url}`);
  return match[1];
}

export function selectVerificationRun(runs, { branch, sha, workflowPath }) {
  return runs.find((item) => item.event === 'push' && item.head_branch === branch && item.head_sha === sha && item.path === workflowPath);
}
