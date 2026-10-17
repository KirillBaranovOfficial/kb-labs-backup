#!/usr/bin/env node
import { spawnSync } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';

const checks = [
  ['Go suite and workflow contract tests', [process.execPath, '--test', 'scripts/ci/go-module-suite.test.mjs']],
  ['Journey unit tests', ['python3', '-m', 'unittest', 'discover', '-s', 'e2e/journey', '-p', 'test_*.py']],
];
const checkName = process.env.KB_DEVKIT_CHECK_NAME;
const workspaceRoot = process.env.KB_DEVKIT_WORKSPACE_ROOT ?? process.cwd();
const issues = [];
const rerun = `Rerun with: kb-devkit check --only ${checkName}`;
const journeyHome = mkdtempSync(path.join(os.tmpdir(), 'kb-ci-clean-machine-home-'));
try {
  for (const [name, [command, ...args]] of checks) {
    const env = name === 'Journey unit tests' ? { ...process.env, HOME: journeyHome } : process.env;
    if (name === 'Journey unit tests') {
      for (const key of ['KB_PLATFORM_ROOT', 'KB_HOME', 'KB_NET_OFFSET', 'GOFLAGS']) delete env[key];
    }
    const result = spawnSync(command, args, { encoding: 'utf8', env });
    if (result.error || result.status !== 0) {
      const detail = [result.error?.message, result.stdout, result.stderr].filter(Boolean).join('\n').trim();
      issues.push({
        check: checkName,
        severity: 'error',
        message: `${name} failed.\n${detail}`,
        fix: rerun,
        file: workspaceRoot,
      });
    }
  }
} finally {
  rmSync(journeyHome, { recursive: true, force: true });
}
process.stdout.write(`${JSON.stringify({ issues })}\n`);
process.exitCode = issues.length === 0 ? 0 : 1;
