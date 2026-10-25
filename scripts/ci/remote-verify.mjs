#!/usr/bin/env node
import { spawnSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { RemoteOperationError, runRemoteVerification } from './remote-verify-core.mjs';
import { parseArgs } from './remote-verify-options.mjs';

const root = process.cwd();

function execute(command, args, { operation = command, commandText = `${command} ${args.join(' ')}`, maxBuffer = 16 * 1024 * 1024 } = {}) {
  const result = spawnSync(command, args, { cwd: root, encoding: 'utf8', maxBuffer });
  if (result.error) throw new RemoteOperationError(`${command} could not run: ${result.error.message}`, { cause: result.error, operation, command: commandText });
  if (result.status !== 0) {
    const diagnostic = (result.stderr || result.stdout || `${command} exited with status ${result.status}`).trim().slice(-2000);
    throw new RemoteOperationError(`${command} operation failed: ${diagnostic}`, { operation, command: commandText });
  }
  return result.stdout.trim();
}

function createExecutors(scratch) {
  const git = {
    headSha: async () => execute('git', ['rev-parse', 'HEAD']),
    backupRemote: async () => execute('git', ['remote', 'get-url', 'backup']),
    pushRef: async (ref, sha) => execute('git', ['push', 'backup', `${sha}:refs/heads/${ref}`], { operation: 'push_ref' }),
    resolveRef: async (remote, ref) => {
      const output = execute('git', ['ls-remote', remote, `refs/heads/${ref}`], { operation: 'resolve_ref' });
      const [resolvedSha] = output.split(/\s+/);
      if (!resolvedSha) throw new RemoteOperationError(`Verification ref ${ref} does not exist on backup`, { operation: 'resolve_ref', command: `git ls-remote ${remote} refs/heads/${ref}` });
      return resolvedSha;
    },
    deleteRef: async (remote, ref) => execute('git', ['push', remote, `:refs/heads/${ref}`], { operation: 'delete_ref' }),
  };

  const gh = {
    listRuns: async (repo, branch) => JSON.parse(execute('gh', ['api', `repos/${repo}/actions/runs?branch=${encodeURIComponent(branch)}&event=push&per_page=100`], { operation: 'run_lookup', commandText: `repos/${repo}/actions/runs?branch=${encodeURIComponent(branch)}&event=push&per_page=100` })).workflow_runs ?? [],
    getRun: async (repo, runId) => JSON.parse(execute('gh', ['api', `repos/${repo}/actions/runs/${runId}`], { operation: 'run_lookup', commandText: `repos/${repo}/actions/runs/${runId}` })),
    downloadVerdict: async (repo, runId) => {
      const dir = path.join(scratch, String(runId));
      mkdirSync(dir, { recursive: true });
      execute('gh', ['run', 'download', String(runId), '--repo', repo, '--name', 'remote-verdict', '--dir', dir], { operation: 'artifact_download', commandText: `gh run download ${runId} --repo ${repo} --name remote-verdict` });
      return JSON.parse(readFileSync(path.join(dir, 'verdict.json'), 'utf8'));
    },
    failureJobs: async (repo, runId) => JSON.parse(execute('gh', ['api', `repos/${repo}/actions/runs/${runId}/jobs?per_page=100`], { operation: 'failure_jobs', commandText: `repos/${repo}/actions/runs/${runId}/jobs?per_page=100` })),
    failureLogs: async (repo, runId) => execute('gh', ['run', 'view', String(runId), '--repo', repo, '--log-failed'], { operation: 'failure_logs', commandText: `gh run view ${runId} --repo ${repo} --log-failed`, maxBuffer: 4 * 1024 * 1024 }).split(/\r?\n/).slice(-120).join('\n').slice(-12_000),
  };
  return { git, gh };
}

async function main() {
  let options;
  try {
    options = parseArgs(process.argv.slice(2), root);
  } catch (error) {
    process.stderr.write(`${error.message}\n`);
    return 2;
  }
  const outDir = path.dirname(options.out);
  const scratch = mkdtempSync(path.join(os.tmpdir(), 'kb-remote-verify-'));
  try {
    const verdict = await runRemoteVerification(options, createExecutors(scratch));
    mkdirSync(outDir, { recursive: true });
    writeFileSync(options.out, `${JSON.stringify(verdict, null, 2)}\n`);
    process.stdout.write(`Remote verification ${verdict.status} for ${verdict.sha}; verdict: ${options.out}\n`);
    return verdict.status === 'pass' ? 0 : 1;
  } catch (error) {
    process.stderr.write(`Remote verification could not write ${options.out}: ${error.message}\n`);
    return 1;
  } finally {
    rmSync(scratch, { recursive: true, force: true });
  }
}

process.exitCode = await main();
