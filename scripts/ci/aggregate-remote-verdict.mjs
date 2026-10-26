#!/usr/bin/env node
import { existsSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { parseVerdict } from './remote-verdict.mjs';

const MISSING_SHARD_LOG = 'Shard did not produce a result artifact; the job was cancelled or failed before its result step.';
const profile = JSON.parse(readFileSync(new URL('./verification-profile.json', import.meta.url), 'utf8'));

function readJSON(file) {
  return JSON.parse(readFileSync(file, 'utf8'));
}

function skippedShard(name) {
  return { name, status: 'skipped', failures: [], logTail: MISSING_SHARD_LOG };
}

export function aggregateVerdict({ sha, build, matrix, results }) {
  const jobs = [build ?? { name: profile.buildStage, status: 'fail', failures: ['Build stage did not produce a result artifact.'], logTail: MISSING_SHARD_LOG }];
  const byName = new Map();
  for (const result of results) {
    if (byName.has(result.name)) throw new Error(`Duplicate remote shard result ${result.name}.`);
    byName.set(result.name, result);
  }
  for (const shard of matrix.include) {
    const name = shard.packages ? `${shard.stage}:shard-${shard.shard}` : shard.stage;
    jobs.push(byName.get(name) ?? skippedShard(name));
  }
  const verdict = {
    schemaVersion: 1,
    sha,
    status: jobs.every((job) => job.status === 'pass') ? 'pass' : 'fail',
    jobs,
    diagnostics: [],
  };
  return parseVerdict(verdict, sha);
}

function main() {
  const [resultsDir, out] = process.argv.slice(2);
  const sha = process.env.GITHUB_SHA;
  const rawMatrix = process.env.REMOTE_TEST_MATRIX || '{"include":[]}';
  if (!resultsDir || !out || !/^[0-9a-f]{40}$/.test(sha ?? '')) throw new Error('Usage: set GITHUB_SHA and REMOTE_TEST_MATRIX, then run node scripts/ci/aggregate-remote-verdict.mjs <results-dir> <output>.');
  const matrix = JSON.parse(rawMatrix);
  const files = existsSync(resultsDir) ? readdirSync(resultsDir).filter((name) => name.endsWith('.json')).map((name) => path.join(resultsDir, name)) : [];
  const results = files.map(readJSON);
  const build = results.find((result) => result.name === profile.buildStage);
  const verdict = aggregateVerdict({ sha, build, matrix, results: results.filter((result) => result !== build) });
  writeFileSync(out, `${JSON.stringify(verdict, null, 2)}\n`);
  if (process.env.GITHUB_STEP_SUMMARY) {
    const rows = verdict.jobs.map((job) => `| ${job.name} | ${job.status} | ${job.durationMs ?? '—'} ms |`).join('\n');
    writeFileSync(process.env.GITHUB_STEP_SUMMARY, `# Remote verification: ${verdict.status}\n\n| Stage | Status | Duration |\n|---|---|---:|\n${rows}\n`);
  }
  process.stdout.write(`Remote verification ${verdict.status} for ${sha}; wrote ${out}.\n`);
  if (verdict.status !== 'pass') process.exitCode = 1;
}

if (process.argv[1] && import.meta.url === new URL(`file://${process.argv[1]}`).href) main();
