#!/usr/bin/env node
import { writeCheckResult } from './lib/check-result.mjs';
/**
 * check-test-pyramid custom check
 *
 * Validates that plugin-entry packages have CLI handler tests:
 *   - src/__tests__/cli/ directory exists
 *   - each src/commands/<name>.ts has a matching src/__tests__/cli/<name>.cli.test.ts
 *   - package.json has a "test:cli" script
 *
 * Output format consumed by kb-devkit's external-check contract:
 *   { issues: [{ check, severity, message, file, fix? }] }
 *
 * Runs with cwd = package directory.
 * Receives package JSON on stdin.
 */

import { existsSync, readdirSync } from 'node:fs';
import { join, basename, extname } from 'node:path';

const cwd = process.cwd();

function readStdin() {
  const chunks = [];
  process.stdin.resume();
  process.stdin.setEncoding('utf-8');
  return new Promise((resolve) => {
    process.stdin.on('data', (c) => chunks.push(c));
    process.stdin.on('end', () => resolve(chunks.join('')));
  });
}

async function main() {
  const raw = await readStdin();
  const input = JSON.parse(raw);
  const pkg = input.package;

  const items = [];
  const commandsDir = join(cwd, 'src/commands');
  const testCliDir = join(cwd, 'src/__tests__/cli');

  // ── 1. Collect commands ─────────────────────────────────────────────────────

  const commandFiles = existsSync(commandsDir)
    ? readdirSync(commandsDir).filter(
        (f) => f.endsWith('.ts') && !f.startsWith('_') && !f.startsWith('index'),
      )
    : [];

  // ── 2. Collect existing test files ─────────────────────────────────────────

  const testFiles = existsSync(testCliDir)
    ? new Set(readdirSync(testCliDir).filter((f) => f.endsWith('.cli.test.ts')))
    : new Set();

  // ── 3. Per-command coverage check ──────────────────────────────────────────

  for (const commandFile of commandFiles) {
    const name = basename(commandFile, extname(commandFile)); // e.g. "task-create"
    const expectedTest = `${name}.cli.test.ts`;

    if (!testFiles.has(expectedTest)) {
      items.push({
        target: `src/__tests__/cli/${expectedTest}`,
        severity: 'warning',
        message: `Command "${name}.ts" has no handler test. Add src/__tests__/cli/${expectedTest}.`,
        fix: `touch src/__tests__/cli/${expectedTest}`,
      });
    }
  }

  // ── 4. No __tests__/cli/ at all (and commands exist) ───────────────────────

  if (commandFiles.length > 0 && !existsSync(testCliDir)) {
    items.push({
      target: 'src/__tests__/cli/',
      severity: 'error',
      message: `No CLI handler tests directory. ${commandFiles.length} command(s) uncovered.`,
      fix: 'mkdir -p src/__tests__/cli',
    });
  }

  // ── 5. Missing test:cli script ─────────────────────────────────────────────

  const scripts = pkg?.scripts ?? {};
  if (!scripts['test:cli']) {
    items.push({
      target: 'package.json#scripts.test:cli',
      severity: 'error',
      message: `Missing "test:cli" script. Add: "test:cli": "vitest run --config vitest.cli.config.ts --passWithNoTests"`,
      fix: 'pnpm pkg set scripts.test:cli="vitest run --config vitest.cli.config.ts --passWithNoTests"',
    });
  }

  const check = process.env.KB_DEVKIT_CHECK_NAME;
  const issues = items.map(({ target, severity, message, fix }) => ({
    check,
    severity,
    message,
    file: target,
    ...(fix ? { fix } : {}),
  }));
  writeCheckResult(issues);
}

main().catch((err) => {
  process.stderr.write(`check-test-pyramid error: ${err.message}\n`);
  process.exit(1);
});
