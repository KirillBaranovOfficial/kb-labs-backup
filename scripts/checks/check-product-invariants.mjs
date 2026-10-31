#!/usr/bin/env node
import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { writeCheckResult } from './lib/check-result.mjs';

const checkName = process.env.KB_DEVKIT_CHECK_NAME;

if (process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1])) {
  const root = process.cwd();
  const documentPath = path.join(root, 'docs/product/invariants.md');
  if (!existsSync(documentPath)) {
    writeCheckResult([{ check: checkName, severity: 'error', file: 'docs/product/invariants.md', message: 'product invariants document does not exist' }]);
    process.exit(1);
  }
  writeCheckResult(productInvariantIssues(readFileSync(documentPath, 'utf8'), root));
}

export function productInvariantIssues(markdown, root) {
  const issues = [];
  const headings = [...markdown.matchAll(/^## (INV-\d{2}) — .+$/gm)];
  for (let index = 0; index < headings.length; index += 1) {
    const heading = headings[index];
    const start = heading.index + heading[0].length;
    const end = headings[index + 1]?.index ?? markdown.length;
    const section = markdown.slice(start, end);
    const line = markdown.slice(0, heading.index).split('\n').length;
    const enforcement = section.match(/^\*\*Enforcement:\*\*\s*(.+)$/m);
    if (!enforcement || enforcement[1].trim() === '') {
      issues.push({
        check: checkName,
        severity: 'error',
        file: 'docs/product/invariants.md',
        line,
        message: `${heading[1]} has no Enforcement entry`,
      });
      continue;
    }

    const links = [...enforcement[1].matchAll(/\[[^\]]+\]\(([^)]+)\)/g)];
    if (links.length === 0) {
      issues.push({
        check: checkName,
        severity: 'error',
        file: 'docs/product/invariants.md',
        line,
        message: `${heading[1]} Enforcement entry must link to a repository path`,
      });
      continue;
    }
    for (const link of links) {
      const target = link[1].split('#', 1)[0];
      if (target === '' || /^[a-z][a-z\d+.-]*:/i.test(target)) continue;
      let decodedTarget;
      try {
        decodedTarget = decodeURIComponent(target);
      } catch {
        issues.push({
          check: checkName,
          severity: 'error',
          file: 'docs/product/invariants.md',
          line,
          message: `${heading[1]} has an invalid encoded Enforcement path: ${target}`,
        });
        continue;
      }
      const resolvedTarget = path.resolve(root, 'docs/product', decodedTarget);
      if (!existsSync(resolvedTarget)) {
        issues.push({
          check: checkName,
          severity: 'error',
          file: 'docs/product/invariants.md',
          line,
          message: `${heading[1]} Enforcement path does not exist: ${target}`,
        });
      }
    }
  }
  return issues;
}
