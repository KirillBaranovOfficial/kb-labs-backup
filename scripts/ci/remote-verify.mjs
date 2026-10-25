#!/usr/bin/env node
import { spawnSync } from 'node:child_process';
import { mkdirSync, readFileSync, writeFileSync, mkdtempSync, rmSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { parseVerdict, repositoryFromRemote, selectVerificationRun, terminalVerdict } from './remote-verdict.mjs';

const root = process.cwd();
const stream = process.argv[2];
const timeoutMs = Number(process.env.REMOTE_VERIFY_TIMEOUT_MS ?? 6 * 60 * 60 * 1000);
const pollMs = Math.max(30_000, Number(process.env.REMOTE_VERIFY_POLL_MS ?? 30_000));
const outputStream = stream && /^[a-zA-Z0-9._-]+$/.test(stream) ? stream : 'invalid';
const outputPath = path.join(root, 'reports', `${outputStream}.remote-verify.json`);
const scratch = mkdtempSync(path.join(os.tmpdir(), 'kb-remote-verify-'));
let ref;
let remote;
let sha;

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: root, encoding: 'utf8', ...options });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} ${args.join(' ')} failed: ${(result.stderr || result.stdout || '').trim()}`);
  return result.stdout.trim();
}

function write(verdict) {
  mkdirSync(path.dirname(outputPath), { recursive: true });
  writeFileSync(outputPath, `${JSON.stringify(verdict, null, 2)}\n`);
}

async function main() {
  if (!stream || !/^[a-zA-Z0-9._-]+$/.test(stream)) throw new Error('Usage: node scripts/ci/remote-verify.mjs <stream>');
  sha = run('git', ['rev-parse', 'HEAD']);
  remote = run('git', ['remote', 'get-url', 'backup']);
  const repo = repositoryFromRemote(remote);
  const created = new Date().toISOString().replace(/[-:]/g, '').replace(/\.\d{3}/, '');
  ref = `verify/${stream}/${created}-${sha}`;
  run('git', ['push', 'backup', `HEAD:refs/heads/${ref}`]);
  const started = Date.now();
  let runInfo;
  while (Date.now() - started < timeoutMs) {
    const response = run('gh', ['api', `repos/${repo}/actions/runs?branch=${encodeURIComponent(ref)}&event=push&per_page=100`]);
    const runs = JSON.parse(response).workflow_runs ?? [];
    runInfo = selectVerificationRun(runs, { branch: ref, sha, workflowPath: '.github/workflows/remote-verify.yml' });
    if (runInfo && runInfo.status === 'completed') break;
    await new Promise((resolve) => setTimeout(resolve, pollMs));
  }
  if (!runInfo || runInfo.status !== 'completed') {
    write(terminalVerdict(sha, 'timeout', `Remote verification did not complete within ${timeoutMs}ms`));
    return 1;
  }
  if (runInfo.conclusion === 'timed_out') {
    write(terminalVerdict(sha, 'timeout', `GitHub Actions run ${runInfo.id} exceeded its workflow timeout`));
    return 1;
  }
  const download = path.join(scratch, 'artifact');
  mkdirSync(download);
  let verdict;
  try {
    run('gh', ['run', 'download', String(runInfo.id), '--repo', repo, '--name', 'remote-verdict', '--dir', download]);
    verdict = parseVerdict(JSON.parse(readFileSync(path.join(download, 'verdict.json'), 'utf8')), sha);
  } catch (error) {
    if (runInfo.conclusion !== 'failure') throw error;
    const jobs = JSON.parse(run('gh', ['api', `repos/${repo}/actions/runs/${runInfo.id}/jobs?per_page=100`])).jobs ?? [];
    const logResult = spawnSync('gh', ['run', 'view', String(runInfo.id), '--repo', repo, '--log-failed'], { cwd: root, encoding: 'utf8', maxBuffer: 4 * 1024 * 1024 });
    const log = [logResult.stdout, logResult.stderr].filter(Boolean).join('\n').split(/\r?\n/).slice(-120).join('\n').slice(-12_000);
    verdict = {
      schemaVersion: 1,
      sha,
      status: 'fail',
      jobs: jobs.map((job) => ({
        name: job.name,
        status: job.conclusion === 'success' ? 'pass' : job.conclusion === 'skipped' ? 'skipped' : 'fail',
        failures: (job.steps ?? []).filter((step) => step.conclusion === 'failure').map((step) => step.name),
        logTail: job.conclusion === 'success' ? '' : log,
      })),
      diagnostics: [`Workflow failed before a verdict artifact was available: ${error.message}`],
    };
    parseVerdict(verdict, sha);
  }
  write(verdict);
  return verdict.status === 'pass' ? 0 : 1;
}

let exitCode = 0;
try {
  exitCode = await main();
} catch (error) {
  const message = error.message || String(error);
  const status = /timeout|timed out/i.test(message) ? 'timeout' : 'outage';
  write(terminalVerdict(sha ?? 'unknown', status, message.slice(-2000)));
  exitCode = 1;
} finally {
  if (remote && ref) {
    const cleanup = spawnSync('git', ['push', 'backup', `:refs/heads/${ref}`], { cwd: root, encoding: 'utf8' });
    if (cleanup.status !== 0) {
      const existing = JSON.parse(readFileSync(outputPath, 'utf8'));
      existing.diagnostics = [...(existing.diagnostics ?? []), `Verify ref cleanup failed: ${(cleanup.stderr || cleanup.stdout || '').trim()}`];
      write(existing);
      exitCode = 1;
    }
  }
  rmSync(scratch, { recursive: true, force: true });
}
process.exitCode = exitCode;
const finalVerdict = JSON.parse(readFileSync(outputPath, 'utf8'));
process.stdout.write(`Remote verification ${finalVerdict.status} for ${finalVerdict.sha}; verdict: ${outputPath}\n`);
