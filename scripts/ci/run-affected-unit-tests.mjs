#!/usr/bin/env node
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { boundFailureUnits, vitestFailures } from './remote-stage-results.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const filters = [
  `...[origin/${process.env.DEFAULT_BRANCH ?? 'master'}]`,
  '!@kb-labs/monorepo',
  '!@kb-labs/e2e',
  '@kb-labs/cli-bin',
];
const selection = await run('corepack', ['pnpm', '-r', ...filters.flatMap((filter) => ['--filter', filter]), 'list', '--json', '--depth=0'], { cwd: root });
if (selection.code !== 0) {
  process.stderr.write(selection.output);
  process.exitCode = selection.code ?? 1;
} else {
  const selected = JSON.parse(selection.stdout);
  const packages = selected.map((entry) => ({
    name: entry.name,
    dir: entry.path,
    vitest: Boolean(entry.devDependencies?.vitest),
    hasTest: Boolean(readPackage(entry.path).scripts?.test),
  })).filter((entry) => entry.hasTest);
  const workspace = mkdtempSync(path.join(os.tmpdir(), 'kb-affected-vitest-'));
  const failures = [];
  let cursor = 0;
  const workers = Array.from({ length: Math.min(2, packages.length) }, async () => {
    while (cursor < packages.length) {
      const pkg = packages[cursor++];
      const reportPath = path.join(workspace, `${cursor}.json`);
      const args = ['pnpm', '--filter', pkg.name, 'test'];
      if (pkg.vitest) args.push('--reporter=json', `--outputFile=${reportPath}`);
      const result = await run('corepack', args, { cwd: root });
      if (result.code === 0) continue;
      if (pkg.vitest) {
        try {
          failures.push(...vitestFailures(JSON.parse(readFileSync(reportPath, 'utf8')), pkg.name));
        } catch (error) {
          failures.push({ name: `${pkg.name} › test`, message: `Vitest JSON report unavailable: ${error.message}\n${result.output}` });
        }
      } else {
        failures.push({ name: `${pkg.name} › test`, message: result.output });
      }
    }
  });
  try {
    await Promise.all(workers);
  } finally {
    rmSync(workspace, { recursive: true, force: true });
  }
  const report = { selectedPackages: packages.length, failureCount: failures.length, failures: boundFailureUnits(failures) };
  process.stdout.write(`${JSON.stringify(report)}\n`);
  process.exitCode = failures.length ? 1 : 0;
}

function readPackage(directory) {
  return JSON.parse(readFileSync(path.join(directory, 'package.json'), 'utf8'));
}

function run(command, args, options) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, { ...options, stdio: ['ignore', 'pipe', 'pipe'] });
    let stdout = '';
    let stderr = '';
    child.stdout.setEncoding('utf8').on('data', (chunk) => { stdout += chunk; });
    child.stderr.setEncoding('utf8').on('data', (chunk) => { stderr += chunk; });
    child.once('error', reject);
    child.once('close', (code, signal) => resolve({ code: signal ? 128 + signalNumber(signal) : code, signal, stdout, stderr, output: [stdout, stderr].filter(Boolean).join('\n') }));
  });
}

function signalNumber(signal) {
  return { SIGINT: 2, SIGTERM: 15 }[signal] ?? 1;
}
