#!/usr/bin/env node
/**
 * check-marketplace-lock — verifies .kb/marketplace.lock has no stale or malformed entries.
 *
 * Devkit external check (TypedCheckOutput v2). Self-filters to @kb-labs/devkit
 * so it runs exactly once (workspace-wide check, same pattern as check-ports
 * and check-assembly-hook).
 *   Input:  KB_DEVKIT_PACKAGE_NAME env or JSON on stdin
 *   Output: { issues: [{ check, severity, message, file }] }
 *
 * Rule: every schema v3 pin has an exact version, integrity, enabled state and
 * source kind, and its key must look like a real package id. Project store
 * paths are derived at runtime and must not be committed here.
 * before it rots silently for months — see the MKT-02 e2e diagnostics test,
 * which reads the same lock via `kb marketplace doctor`.
 *
 * Fix stale entries: `kb marketplace plugins link`/`unlink` the package again,
 * or remove the offending key from `.kb/marketplace.lock` by hand for a one-off.
 *
 * Standalone: node scripts/checks/check-marketplace-lock.mjs
 */

import { readFileSync, existsSync } from 'node:fs';
import { join } from 'node:path';

const CHECK_NAME = 'check-marketplace-lock';
const ANCHOR_PACKAGE = '@kb-labs/devkit';
const DEVKIT_MODE = Boolean(process.env.KB_DEVKIT_MODE);

const ROOT = process.env.KB_MARKETPLACE_LOCK_ROOT ?? new URL('../../', import.meta.url).pathname.replace(/\/$/, '');
const LOCK_PATH = join(ROOT, '.kb', 'marketplace.lock');

// @scope/name or bare name — no colons, no trailing slash, no path separators.
const VALID_ID = /^(@[a-z0-9][a-z0-9._-]*\/)?[a-z0-9][a-z0-9._-]*$/i;

function validate() {
  const issues = [];

  if (!existsSync(LOCK_PATH)) {
    return issues; // no lock file — nothing to check
  }

  let lock;
  try {
    lock = JSON.parse(readFileSync(LOCK_PATH, 'utf-8'));
  } catch (err) {
    issues.push({
      check: CHECK_NAME,
      severity: 'error',
      message: `.kb/marketplace.lock is not valid JSON: ${err.message}`,
      file: '.kb/marketplace.lock',
    });
    return issues;
  }

  if (lock.schema !== 'kb.marketplace/3') {
    const v2 = lock.schema === 'kb.marketplace/2';
    issues.push({
      check: CHECK_NAME,
      severity: 'error',
      message: v2
        ? '.kb/marketplace.lock uses schema v2; run "kb marketplace sync" to regenerate it as v3.'
        : '.kb/marketplace.lock must use schema kb.marketplace/3.',
      file: '.kb/marketplace.lock',
    });
    return issues;
  }

  if ('adapters' in lock) {
    issues.push({ check: CHECK_NAME, severity: 'error', message: '.kb/marketplace.lock is a project lock and must not contain a machine-level adapters map.', file: '.kb/marketplace.lock' });
    return issues;
  }

  const plugins = lock.plugins ?? {};

  for (const [id, entry] of Object.entries(plugins)) {
    if (!VALID_ID.test(id)) {
      issues.push({
        check: CHECK_NAME,
        severity: 'error',
        message: `.kb/marketplace.lock: malformed package id "${id}" — expected "name" or "@scope/name". Likely a corrupted install; remove or re-install this entry.`,
        file: '.kb/marketplace.lock',
      });
      continue;
    }

    if (entry?.source === 'marketplace' && (
      typeof entry.version !== 'string' || typeof entry.integrity !== 'string' ||
      typeof entry.enabled !== 'boolean' || 'resolvedPath' in entry || 'installedAt' in entry
    )) {
      issues.push({
        check: CHECK_NAME,
        severity: 'error',
        message: `.kb/marketplace.lock: "${id}" is not a machine-independent v3 pin (expected version, integrity, enabled, source; no resolvedPath or installedAt).`,
        file: '.kb/marketplace.lock',
      });
      continue;
    }
    if (entry?.source === 'local' && (typeof entry.link !== 'string' || entry.link.startsWith('/') || typeof entry.enabled !== 'boolean')) {
      issues.push({
        check: CHECK_NAME,
        severity: 'error',
        message: `.kb/marketplace.lock: "${id}" has an invalid local link entry.`,
        file: '.kb/marketplace.lock',
      });
      continue;
    }
    if (!['marketplace', 'local'].includes(entry?.source)) {
      issues.push({
        check: CHECK_NAME,
        severity: 'error',
        message: `.kb/marketplace.lock: "${id}" has an unsupported project source kind.`,
        file: '.kb/marketplace.lock',
      });
      continue;
    }
    const allowed = entry.source === 'marketplace'
      ? ['version', 'integrity', 'enabled', 'source']
      : ['link', 'enabled', 'source'];
    if (Object.keys(entry ?? {}).some((key) => !allowed.includes(key))) {
      issues.push({ check: CHECK_NAME, severity: 'error', message: `.kb/marketplace.lock: "${id}" contains fields outside the project v3 lock contract.`, file: '.kb/marketplace.lock' });
    }
  }

  return issues;
}

function printHuman(issues) {
  if (issues.length === 0) {
    process.stdout.write(`${CHECK_NAME}: OK — marketplace.lock entries use schema v3\n`);
    return;
  }
  for (const i of issues) {
    process.stderr.write(`${i.severity.toUpperCase()}: ${i.message}\n`);
  }
  const errors = issues.filter((i) => i.severity === 'error').length;
  if (errors > 0) {
    process.stderr.write(`\n${CHECK_NAME}: ${errors} error(s) found\n`);
  }
}

if (DEVKIT_MODE) {
  let pkgName = process.env.KB_DEVKIT_PACKAGE_NAME ?? '';
  if (!pkgName) {
    try {
      const chunks = [];
      process.stdin.resume();
      process.stdin.setEncoding('utf-8');
      await new Promise((resolve) => {
        process.stdin.on('data', (c) => chunks.push(c));
        process.stdin.on('end', () => resolve());
        setTimeout(() => resolve(), 50);
      });
      const raw = chunks.join('');
      if (raw) pkgName = JSON.parse(raw)?.name ?? '';
    } catch { /* no JSON on stdin */ }
  }

  if (pkgName !== ANCHOR_PACKAGE) {
    process.stdout.write(JSON.stringify({ issues: [] }) + '\n');
    process.exit(0);
  }

  const issues = validate();
  process.stdout.write(JSON.stringify({ issues }) + '\n');
  process.exit(0); // devkit aggregates severity; never hard-fail the runner
} else {
  const issues = validate();
  printHuman(issues);
  process.exit(issues.some((i) => i.severity === 'error') ? 1 : 0);
}
