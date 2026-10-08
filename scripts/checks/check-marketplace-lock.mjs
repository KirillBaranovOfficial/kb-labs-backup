#!/usr/bin/env node
/**
 * check-marketplace-lock — verifies the project lock with core-discovery's
 * runtime validator and reports project-scope remediation.
 */

import { readFileSync, existsSync } from 'node:fs';
import { join } from 'node:path';
const { validateMarketplaceLock } = await import('@kb-labs/core-discovery');

const CHECK_NAME = 'check-marketplace-lock';
const ANCHOR_PACKAGE = '@kb-labs/devkit';
const DEVKIT_MODE = Boolean(process.env.KB_DEVKIT_MODE);
const ROOT = process.env.KB_MARKETPLACE_LOCK_ROOT ?? new URL('../../', import.meta.url).pathname.replace(/\/$/, '');
const LOCK_PATH = join(ROOT, '.kb', 'marketplace.lock');
const REMEDIATION = 'Run "kb marketplace sync" to regenerate .kb/marketplace.lock.';

function validate() {
  if (!existsSync(LOCK_PATH)) {return [];}

  let lock;
  try {
    lock = JSON.parse(readFileSync(LOCK_PATH, 'utf-8'));
  } catch (error) {
    return [issue(`.kb/marketplace.lock is not valid JSON: ${error.message}`)];
  }

  const validation = validateMarketplaceLock(lock, 'project');
  if (!validation.valid) {
    const subject = validation.entry ? `"${validation.entry}"` : '.kb/marketplace.lock';
    return [issue(`${subject}: ${validation.reason}. ${REMEDIATION}`)];
  }

  return [];
}

function issue(message) {
  return { check: CHECK_NAME, severity: 'error', message, file: '.kb/marketplace.lock' };
}

function printHuman(issues) {
  if (issues.length === 0) {
    process.stdout.write(`${CHECK_NAME}: OK — marketplace.lock entries use schema v3\n`);
    return;
  }
  for (const item of issues) {process.stderr.write(`ERROR: ${item.message}\n`);}
  process.stderr.write(`\n${CHECK_NAME}: ${issues.length} error(s) found\n`);
}

if (DEVKIT_MODE) {
  let pkgName = process.env.KB_DEVKIT_PACKAGE_NAME ?? '';
  if (!pkgName) {
    const chunks = [];
    process.stdin.setEncoding('utf-8');
    for await (const chunk of process.stdin) {chunks.push(chunk);}
    const raw = chunks.join('');
    if (raw) {pkgName = JSON.parse(raw)?.name ?? '';}
  }
  if (pkgName !== ANCHOR_PACKAGE) {
    process.stdout.write(JSON.stringify({ issues: [] }) + '\n');
  } else {
    process.stdout.write(JSON.stringify({ issues: validate() }) + '\n');
  }
} else {
  const issues = validate();
  printHuman(issues);
  process.exit(issues.some((item) => item.severity === 'error') ? 1 : 0);
}
