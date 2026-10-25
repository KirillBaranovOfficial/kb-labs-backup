import { parseVerdict, repositoryFromRemote, selectVerificationRun } from './remote-verdict.mjs';

const WORKFLOW_PATH = '.github/workflows/remote-verify.yml';
const MAX_RETRY_DELAY_MS = 60_000;

export class RemoteOperationError extends Error {
  constructor(message, { retryable = false, cause, operation, command } = {}) {
    super(message, { cause });
    this.name = 'RemoteOperationError';
    this.retryable = retryable;
    this.operation = operation;
    this.command = command;
  }
}

export class RemoteTimeoutError extends Error {
  constructor(message = 'Remote verification deadline expired', causeDetails) {
    super(message);
    this.name = 'RemoteTimeoutError';
    this.causeDetails = causeDetails;
  }
}

export class RemoteOutageError extends Error {
  constructor(message = 'GitHub or git remained unavailable beyond the outage budget', causeDetails) {
    super(message);
    this.name = 'RemoteOutageError';
    this.causeDetails = causeDetails;
  }
}

function operationCause(operation, command, attempts, startedAtMs, endedAtMs, lastError) {
  const boundedError = String(lastError || 'No operation error was recorded').slice(0, 1_000);
  return {
    operation,
    command: String(command).slice(0, 1_000),
    attempts,
    startedAtMs,
    endedAtMs,
    elapsedMs: Math.max(0, endedAtMs - startedAtMs),
    lastError: boundedError,
  };
}

export async function retryOperation(operation, { operationName, command, deadlineAt, outageBudgetMs, now, sleep, callRemote, cadenceMs }) {
  let outageStartedAt;
  let attempt = 0;
  const startedAtMs = now();
  let lastError = 'Deadline expired before the operation was attempted';
  const causeDetails = (endedAtMs = now()) => operationCause(operationName, command, attempt, startedAtMs, endedAtMs, lastError);
  while (true) {
    if (now() >= deadlineAt) throw new RemoteTimeoutError('Remote verification deadline expired', causeDetails());
    if (outageStartedAt !== undefined && now() - outageStartedAt >= outageBudgetMs) throw new RemoteOutageError('GitHub or git remained unavailable beyond the outage budget', causeDetails());
    try {
      return await callRemote(async () => {
        attempt += 1;
        return operation();
      }, deadlineAt);
    } catch (error) {
      lastError = error?.message ?? String(error);
      if (error && typeof error === 'object') error.causeDetails = causeDetails();
      if (error?.retryable !== true) throw error;
      const failedAt = now();
      outageStartedAt ??= failedAt;
      if (failedAt >= deadlineAt) throw new RemoteTimeoutError('Remote verification deadline expired', causeDetails(failedAt));
      if (failedAt - outageStartedAt >= outageBudgetMs) throw new RemoteOutageError('GitHub or git remained unavailable beyond the outage budget', causeDetails(failedAt));
      const outageLeft = outageBudgetMs - (failedAt - outageStartedAt);
      const deadlineLeft = deadlineAt - failedAt;
      const delay = Math.min(cadenceMs * (2 ** attempt), MAX_RETRY_DELAY_MS, outageLeft, deadlineLeft);
      await sleep(delay);
    }
  }
}

function createRemoteCaller(clock, cadenceMs) {
  let lastCallAt;
  return async (operation, deadlineAt) => {
    if (lastCallAt !== undefined) {
      const waitMs = Math.max(0, cadenceMs - (clock.now() - lastCallAt));
      if (waitMs > 0) {
        const remaining = deadlineAt - clock.now();
        if (waitMs >= remaining) {
          await clock.sleep(Math.max(remaining, 0));
          throw new RemoteTimeoutError('Remote verification deadline expired');
        }
        await clock.sleep(waitMs);
      }
    }
    if (clock.now() >= deadlineAt) throw new RemoteTimeoutError('Remote verification deadline expired');
    try {
      const result = await operation();
      if (clock.now() >= deadlineAt) throw new RemoteTimeoutError();
      return result;
    } finally {
      lastCallAt = clock.now();
    }
  };
}

function remotePolicy(overrides) {
  const pollMs = Math.min(60_000, Math.max(30_000, overrides.pollMs ?? 45_000));
  return {
    timeoutMs: 6 * 60 * 60 * 1000,
    outageBudgetMs: 5 * 60 * 1000,
    cleanupBudgetMs: 180_000,
    ...overrides,
    pollMs,
  };
}

async function waitForRun(lookup, { deadlineAt, policy, clock, callRemote, initialRun, command }) {
  let run = initialRun;
  while (true) {
    if (run?.status === 'completed') return run;
    run = await retryOperation(lookup, {
      operationName: 'run_lookup',
      command,
      deadlineAt,
      outageBudgetMs: policy.outageBudgetMs,
      now: clock.now,
      sleep: clock.sleep,
      callRemote,
      cadenceMs: policy.pollMs,
    });
  }
}

function validateRun(run, expectedBranch) {
  if (!run || run.event !== 'push' || run.path !== WORKFLOW_PATH || !/^verify\//.test(run.head_branch ?? '') || !/^[0-9a-f]{40}$/.test(run.head_sha ?? '')) {
    throw new Error('Attached run is not a valid remote verification push run');
  }
  if (expectedBranch && run.head_branch !== expectedBranch) throw new Error('Attached run branch does not match the requested verification ref');
  return run;
}

function failureVerdict(sha, diagnostics, details = { jobs: [] }) {
  return { schemaVersion: 1, sha, status: 'fail', jobs: details.jobs ?? [], diagnostics: [...diagnostics, ...(details.diagnostics ?? [])] };
}

function classifyError(error, sha) {
  const cause = error?.causeDetails ?? operationCause(error?.operation ?? 'verification', error?.command ?? 'remote verification', 1, 0, 0, error?.message ?? String(error));
  if (error instanceof RemoteTimeoutError) return { schemaVersion: 1, sha: sha ?? 'unknown', status: 'timeout', jobs: [], diagnostics: [error.message], cause };
  if (error instanceof RemoteOutageError) return { schemaVersion: 1, sha: sha ?? 'unknown', status: 'outage', jobs: [], diagnostics: [error.message], cause };
  return { ...failureVerdict(sha ?? 'unknown', [error?.message ?? String(error)]), cause };
}

function failureDetails(jobsResponse, logTail) {
  return {
    jobs: (jobsResponse.jobs ?? []).map((job) => ({
      name: job.name,
      status: job.conclusion === 'success' ? 'pass' : job.conclusion === 'skipped' ? 'skipped' : 'fail',
      failures: (job.steps ?? []).filter((step) => step.conclusion === 'failure').map((step) => step.name),
      logTail: job.conclusion === 'success' ? '' : logTail,
    })),
  };
}

export async function runRemoteVerification(options, executors, overrides = {}) {
  if (!options?.out) throw new Error('The --out path is required');
  if (Boolean(options.attach) === Boolean(options.stream)) throw new Error('Provide exactly one stream name or --attach value');
  const gh = executors.gh;
  const git = executors.git;
  const clock = overrides.clock ?? { now: Date.now, sleep: (ms) => new Promise((resolve) => setTimeout(resolve, ms)) };
  const policy = remotePolicy(overrides.policy ?? {});
  const deadlineAt = clock.now() + policy.timeoutMs;
  const callRemote = createRemoteCaller(clock, policy.pollMs);
  let repo;
  let remote;
  let ref;
  let sha;
  let run;
  let verdict;

  try {
    remote = await git.backupRemote();
    repo = repositoryFromRemote(remote);
    if (options.attach) {
      if (/^\d+$/.test(options.attach)) {
        run = validateRun(await retryOperation(() => gh.getRun(repo, options.attach), {
          operationName: 'run_lookup',
          command: `repos/${repo}/actions/runs/${options.attach}`,
          deadlineAt, outageBudgetMs: policy.outageBudgetMs, now: clock.now, sleep: clock.sleep,
          callRemote, cadenceMs: policy.pollMs,
        }));
        sha = run.head_sha;
        ref = run.head_branch;
        run = await waitForRun(() => gh.getRun(repo, options.attach), { deadlineAt, policy, clock, callRemote, initialRun: run, command: `repos/${repo}/actions/runs/${options.attach}` });
        validateRun(run, ref);
      } else {
        ref = options.attach;
        if (!ref.startsWith('verify/')) throw new Error('An attached ref must be under verify/');
        const namedSha = ref.match(/\/(?:[0-9]{8}T[0-9]{6}Z)-([0-9a-f]{40})$/)?.[1];
        if (!namedSha) throw new Error('Attached verification ref must include its timestamp and commit SHA');
        sha = await retryOperation(() => git.resolveRef(remote, ref), {
          operationName: 'resolve_ref',
          command: `git ls-remote ${remote} refs/heads/${ref}`,
          deadlineAt, outageBudgetMs: policy.outageBudgetMs, now: clock.now, sleep: clock.sleep,
          callRemote, cadenceMs: policy.pollMs,
        });
        if (!/^[0-9a-f]{40}$/.test(sha)) throw new Error('Attached verification ref did not resolve to a full commit SHA');
        if (sha !== namedSha) throw new Error(`Attached ref SHA mismatch: ref names ${namedSha}, remote ref resolves to ${sha}`);
        run = await waitForRun(async () => selectVerificationRun(await gh.listRuns(repo, ref), { branch: ref, sha, workflowPath: WORKFLOW_PATH }), { deadlineAt, policy, clock, callRemote, command: `repos/${repo}/actions/runs?branch=${encodeURIComponent(ref)}&event=push&per_page=100` });
        validateRun(run, ref);
      }
    } else {
      sha = await git.headSha();
      if (!/^[0-9a-f]{40}$/.test(sha)) throw new Error('Current HEAD did not resolve to a full commit SHA');
      const created = new Date(clock.now()).toISOString().replace(/[-:]/g, '').replace(/\.\d{3}/, '');
      ref = `verify/${options.stream}/${created}-${sha}`;
      await retryOperation(() => git.pushRef(ref, sha), {
        operationName: 'push_ref',
        command: `git push backup ${sha}:refs/heads/${ref}`,
        deadlineAt, outageBudgetMs: policy.outageBudgetMs, now: clock.now, sleep: clock.sleep,
        callRemote, cadenceMs: policy.pollMs,
      });
      run = await waitForRun(async () => selectVerificationRun(await gh.listRuns(repo, ref), { branch: ref, sha, workflowPath: WORKFLOW_PATH }), { deadlineAt, policy, clock, callRemote, command: `repos/${repo}/actions/runs?branch=${encodeURIComponent(ref)}&event=push&per_page=100` });
      validateRun(run, ref);
    }

    let artifact;
    let artifactError;
    try {
      artifact = await callRemote(() => gh.downloadVerdict(repo, run.id), deadlineAt);
    } catch (error) {
      if (error instanceof RemoteTimeoutError) {
        error.causeDetails = operationCause('artifact_download', `gh run download ${run.id} --repo ${repo} --name remote-verdict`, 0, clock.now(), clock.now(), error.message);
        throw error;
      }
      if (error && typeof error === 'object') error.causeDetails = operationCause('artifact_download', `gh run download ${run.id} --repo ${repo} --name remote-verdict`, 1, clock.now(), clock.now(), error.message);
      artifactError = error;
    }
    if (run.conclusion === 'timed_out') {
      const now = clock.now();
      verdict = {
        schemaVersion: 1, sha, status: 'timeout', jobs: [],
        diagnostics: [`GitHub Actions run ${run.id} exceeded its workflow timeout`, ...(artifactError ? [`The completed run artifact could not be fetched once: ${artifactError.message}`] : [])],
        cause: operationCause('workflow_result', `repos/${repo}/actions/runs/${run.id}`, 1, now, now, `GitHub Actions run ${run.id} concluded timed_out`),
      };
    } else {
      try {
        if (artifactError) throw artifactError;
        const parsed = parseVerdict(artifact, sha);
        verdict = {
          ...parsed,
          jobs: parsed.jobs.map((job) => ({ ...job, failures: [...job.failures] })),
          diagnostics: [...(parsed.diagnostics ?? [])],
        };
      } catch (error) {
        if (error instanceof RemoteTimeoutError) throw error;
        if (error && typeof error === 'object' && !error.causeDetails) {
          error.causeDetails = operationCause('artifact_validation', `remote-verdict artifact for run ${run.id}`, 1, clock.now(), clock.now(), error.message);
        }
        if (run.conclusion === 'failure') {
          try {
            const jobsResponse = await retryOperation(() => gh.failureJobs(repo, run.id), {
              operationName: 'failure_jobs',
              command: `repos/${repo}/actions/runs/${run.id}/jobs?per_page=100`,
              deadlineAt, outageBudgetMs: policy.outageBudgetMs, now: clock.now, sleep: clock.sleep,
              callRemote, cadenceMs: policy.pollMs,
            });
            const logTail = await retryOperation(() => gh.failureLogs(repo, run.id), {
              operationName: 'failure_logs',
              command: `gh run view ${run.id} --repo ${repo} --log-failed`,
              deadlineAt, outageBudgetMs: policy.outageBudgetMs, now: clock.now, sleep: clock.sleep,
              callRemote, cadenceMs: policy.pollMs,
            });
            verdict = failureVerdict(sha, [`Workflow failed before a valid verdict artifact was available: ${error.message}`], failureDetails(jobsResponse, logTail));
          } catch (detailsError) {
            if (detailsError instanceof RemoteTimeoutError || detailsError instanceof RemoteOutageError) throw detailsError;
            verdict = failureVerdict(sha, [`Workflow failed and diagnostics could not be retrieved: ${detailsError.message}`]);
          }
          if (error instanceof RemoteOutageError && verdict.status === 'fail') {
            verdict.diagnostics.push(`Verdict artifact retrieval failed: ${error.message}`);
          }
        } else {
          verdict = failureVerdict(sha, [`Could not retrieve a valid verdict artifact once: ${error.message}`]);
          verdict.cause = error.causeDetails;
        }
      }
      if (run.conclusion !== 'success' && verdict.status === 'pass') {
        verdict = failureVerdict(sha, [`GitHub Actions concluded ${run.conclusion}; a passing artifact cannot override that result`], { jobs: verdict.jobs });
      }
    }
  } catch (error) {
    verdict = classifyError(error, sha);
  }

  verdict.diagnostics ??= [];
  if (verdict.status !== 'pass' && !verdict.cause) {
    const now = clock.now();
    verdict.cause = operationCause('workflow_result', run ? `repos/${repo}/actions/runs/${run.id}` : 'remote verification', 1, now, now, verdict.diagnostics[0] ?? 'Remote verification failed');
  }
  if (ref && remote) {
    try {
      await retryOperation(() => git.deleteRef(remote, ref), {
        operationName: 'delete_ref',
        command: `git push ${remote} :refs/heads/${ref}`,
        deadlineAt: clock.now() + policy.cleanupBudgetMs,
        outageBudgetMs: policy.cleanupBudgetMs,
        now: clock.now,
        sleep: clock.sleep,
        callRemote,
        cadenceMs: policy.pollMs,
      });
    } catch (error) {
      verdict.diagnostics.push(`Verify ref cleanup failed after retries; the scheduled sweep will remove ${ref}: ${error.message}`);
      if (verdict.status === 'pass') verdict.cause = error.causeDetails ?? operationCause('delete_ref', `git push ${remote} :refs/heads/${ref}`, 1, clock.now(), clock.now(), error.message);
    }
  }
  return verdict;
}

