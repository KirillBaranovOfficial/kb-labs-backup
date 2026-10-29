#!/usr/bin/env node
import { existsSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import path from 'node:path';
import { parseVerdict } from './remote-verdict.mjs';
import { resourcePeaks } from './remote-build-resources.mjs';

const profile = JSON.parse(readFileSync(new URL('./verification-profile.json', import.meta.url), 'utf8'));
const MISSING_SHARD_LOG = 'Shard did not produce a result artifact.';

function readJSON(file) {
  return JSON.parse(readFileSync(file, 'utf8'));
}

function githubJobName(shard) {
  const independent = Object.values(profile.independentTestStages).some((stages) => stages.includes(shard.stage));
  return `${independent ? 'Independent' : 'Build-dependent'} test ${shard.shard ?? shard.stage}`;
}

function missingShard(shard, selectedPackageCount, githubJobs, githubJobsError, results, verificationProfile) {
  const name = shard.packages ? `${shard.stage}:shard-${shard.shard}` : shard.stage;
  const count = shard.packages ? shard.packages.length : selectedPackageCount;
  if (githubJobsError) {
    return { name, status: 'fail', selectedPackageCount: count, failures: [`GitHub job evidence API read failed: ${githubJobsError}`], logTail: '' };
  }
  const evidence = githubJobs.find((job) => job.name === githubJobName(shard));
  if (!evidence) {
    return { name, status: 'fail', selectedPackageCount: count, failures: [`GitHub job evidence is missing for planned job ${githubJobName(shard)}.`], logTail: '' };
  }
  const steps = (evidence.steps ?? []).map(({ name: stepName, conclusion, status }) => ({ name: stepName, ...(conclusion === undefined ? {} : { conclusion }), ...(status === undefined ? {} : { status }) }));
  const annotations = (evidence.annotations ?? []).map((annotation) => annotation.message ?? JSON.stringify(annotation));
  const conclusion = evidence.conclusion ?? 'unknown';
  if (conclusion === 'skipped') {
    return { name, status: 'skipped', selectedPackageCount: count, failures: ['GitHub skipped this planned job.'], logTail: 'GitHub skipped this planned job.', githubConclusion: conclusion, steps };
  }
  if (conclusion === 'cancelled' && verificationProfile === 'stream') {
    const group = profile.independentTestStages.stream.includes(shard.stage) ? 'independent' : 'build-dependent';
    const failedPeer = results.some((result) => {
      const peer = shard.packages ? result.name.startsWith(`${shard.stage}:shard-`) : profile.independentTestStages.stream.includes(result.name) || profile.buildDependentTestStages.stream.includes(result.name);
      if (!peer || result.status !== 'fail') return false;
      const peerGroup = profile.independentTestStages.stream.includes(result.name) ? 'independent' : 'build-dependent';
      return peerGroup === group;
    });
    if (failedPeer) {
      const reason = 'cancelled by fail-fast after a peer in the same test matrix failed';
      return { name, status: 'skipped', selectedPackageCount: count, failures: [reason], logTail: reason, githubConclusion: conclusion, steps };
    }
  }
  const timedOut = conclusion === 'timed_out';
  const status = timedOut ? 'timeout' : 'fail';
  const failures = [...annotations];
  if (timedOut) failures.unshift(`GitHub job ${githubJobName(shard)} timed out.`);
  else if (!failures.length) failures.push(`GitHub job ${githubJobName(shard)} concluded ${conclusion} without a result artifact.`);
  return {
    name, status, selectedPackageCount: count, failures,
    logTail: `GitHub conclusion: ${conclusion}; steps: ${JSON.stringify(steps)}`,
    githubConclusion: conclusion, steps,
  };
}

export function aggregateVerdict({
  sha, prepare, prepareJobResult = 'unknown', build, buildJobResult = 'unknown', buildResourceSamples = [],
  matrix, results, selectedPackageCount, verificationProfile = 'stream', githubJobs = [], githubJobsError,
}) {
  if (!Number.isSafeInteger(selectedPackageCount) || selectedPackageCount < 0) throw new Error('Remote verdict requires the selected package count from the prepare job.');
  const prepareStage = prepare ?? {
    name: 'prepare',
    status: 'fail',
    selectedPackageCount,
    failures: [`Prepare job concluded ${prepareJobResult} without a prepare stage result artifact.`],
    logTail: MISSING_SHARD_LOG,
  };
  let buildStage = build;
  if (!buildStage || buildStage.status === 'running') {
    const samples = [...(buildStage?.resourceSamples ?? []), ...buildResourceSamples];
    const terminated = ['cancelled', 'timed_out', 'failure'].includes(buildJobResult);
    const cause = buildJobResult === 'timed_out'
      ? 'runner-timeout'
      : buildJobResult === 'cancelled' || (buildJobResult === 'failure' && samples.length > 0)
        ? 'runner-shutdown'
        : 'build-result-missing';
    const lastResourceSample = buildResourceSamples.at(-1) ?? buildStage?.lastResourceSample ?? samples.at(-1);
    buildStage = {
      name: profile.buildStage,
      status: 'fail',
      selectedPackageCount: buildStage?.selectedPackageCount ?? selectedPackageCount,
      failures: [...(buildStage?.failures ?? []), `${cause}: workspace build job concluded ${buildJobResult}${terminated ? ' before completing its stage result' : ' without a completed stage result'}.`],
      logTail: lastResourceSample ? `Last resource sample: ${JSON.stringify(lastResourceSample)}` : MISSING_SHARD_LOG,
      ...(buildStage?.durationMs === undefined ? {} : { durationMs: buildStage.durationMs }),
      ...(lastResourceSample ? { lastResourceSample } : {}),
      ...(samples.length ? { resourcePeaks: resourcePeaks(samples) } : {}),
    };
  } else if (buildResourceSamples.length) {
    const samples = [...(buildStage.resourceSamples ?? []), ...buildResourceSamples];
    buildStage = { ...buildStage, resourceSamples: samples, resourcePeaks: resourcePeaks(samples), lastResourceSample: samples.at(-1) };
  }
  const jobs = [prepareStage, buildStage ?? { name: profile.buildStage, status: 'fail', selectedPackageCount, failures: ['build-result-missing: no build stage result artifact.'], logTail: MISSING_SHARD_LOG }];
  const byName = new Map();
  for (const result of results) {
    if (byName.has(result.name)) throw new Error(`Duplicate remote shard result ${result.name}.`);
    byName.set(result.name, result);
  }
  for (const shard of matrix.include) {
    const name = shard.packages ? `${shard.stage}:shard-${shard.shard}` : shard.stage;
    jobs.push(byName.get(name) ?? missingShard(shard, selectedPackageCount, githubJobs, githubJobsError, jobs.slice(2), verificationProfile));
  }
  for (const job of jobs) {
    if (!Number.isSafeInteger(job.selectedPackageCount) || job.selectedPackageCount < 0) throw new Error(`Remote verdict stage ${job.name} is missing its selected package count.`);
  }
  const verdict = {
    schemaVersion: 1,
    sha,
    profile: verificationProfile,
    status: jobs.every((job) => job.status === 'pass') ? 'pass' : 'fail',
    jobs,
    diagnostics: [],
  };
  return parseVerdict(verdict, sha);
}

export function fetchGitHubJobEvidence({ repository, runId, plannedJobs, runCommand }) {
  const jobsUrl = `repos/${repository}/actions/runs/${runId}/jobs?per_page=100`;
  const jobs = JSON.parse(runCommand(jobsUrl)).jobs ?? [];
  const missingNames = new Set(plannedJobs.map(githubJobName));
  return jobs.filter((job) => missingNames.has(job.name)).map((job) => {
    const checkRunId = job.check_run_url?.split('/').at(-1);
    const annotations = checkRunId
      ? JSON.parse(runCommand(`repos/${repository}/check-runs/${checkRunId}/annotations?per_page=100`))
      : [];
    return { name: job.name, conclusion: job.conclusion, steps: job.steps ?? [], annotations };
  });
}

function main() {
  const [resultsDir, out] = process.argv.slice(2);
  const sha = process.env.GITHUB_SHA;
  const selectedPackageCount = Number(process.env.REMOTE_SELECTED_PACKAGE_COUNT);
  const rawMatrix = process.env.REMOTE_TEST_MATRIX;
  if (!resultsDir || !out || !/^[0-9a-f]{40}$/.test(sha ?? '') || !Number.isSafeInteger(selectedPackageCount) || selectedPackageCount < 0) throw new Error('Usage: set GITHUB_SHA and REMOTE_SELECTED_PACKAGE_COUNT, then run node scripts/ci/aggregate-remote-verdict.mjs <results-dir> <output>.');
  const matrix = rawMatrix ? JSON.parse(rawMatrix) : { include: [] };
  const files = existsSync(resultsDir) ? readdirSync(resultsDir).filter((name) => name.endsWith('.json')).map((name) => path.join(resultsDir, name)) : [];
  const results = files.map(readJSON);
  const prepare = results.find((result) => result.name === 'prepare');
  const build = results.find((result) => result.name === profile.buildStage);
  const resourceSampleFile = process.env.BUILD_RESOURCE_SAMPLES;
  const buildResourceSamples = resourceSampleFile && existsSync(resourceSampleFile)
    ? readFileSync(resourceSampleFile, 'utf8').split(/\r?\n/).filter(Boolean).map((line) => JSON.parse(line))
    : [];
  let githubJobs = [];
  let githubJobsError;
  const resultNames = new Set(results.map((result) => result.name));
  const missingPlannedJobs = matrix.include.filter((shard) => !resultNames.has(shard.packages ? `${shard.stage}:shard-${shard.shard}` : shard.stage));
  if (missingPlannedJobs.length) {
    try {
      githubJobs = fetchGitHubJobEvidence({
        repository: process.env.GITHUB_REPOSITORY,
        runId: process.env.GITHUB_RUN_ID,
        plannedJobs: missingPlannedJobs,
        runCommand: (endpoint) => {
          const result = spawnSync('gh', ['api', endpoint], { encoding: 'utf8', maxBuffer: 16 * 1024 * 1024 });
          if (result.error) throw result.error;
          if (result.status !== 0) throw new Error((result.stderr || result.stdout || `gh api exited ${result.status}`).trim());
          return result.stdout;
        },
      });
    } catch (error) {
      githubJobsError = error.message;
    }
  }
  const verdict = aggregateVerdict({
    sha, prepare, prepareJobResult: process.env.PREPARE_JOB_RESULT, build,
    buildJobResult: process.env.BUILD_JOB_RESULT, buildResourceSamples, matrix,
    results: results.filter((result) => result !== prepare && result !== build), selectedPackageCount,
    githubJobs, githubJobsError,
    verificationProfile: process.env.VERIFICATION_PROFILE ?? 'stream',
  });
  writeFileSync(out, `${JSON.stringify(verdict, null, 2)}\n`);
  if (process.env.GITHUB_STEP_SUMMARY) {
    const rows = verdict.jobs.map((job) => `| ${job.name} | ${job.status} | ${job.durationMs ?? '—'} ms |`).join('\n');
    writeFileSync(process.env.GITHUB_STEP_SUMMARY, `# Remote verification: ${verdict.status}\n\n| Stage | Status | Duration |\n|---|---|---:|\n${rows}\n`);
  }
  process.stdout.write(`Remote verification ${verdict.status} for ${sha}; wrote ${out}.\n`);
  if (verdict.status !== 'pass') process.exitCode = 1;
}

if (process.argv[1] && import.meta.url === new URL(`file://${process.argv[1]}`).href) main();
