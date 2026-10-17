#!/usr/bin/env node
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { releasePackageNames } from '../../e2e/platform/journey-update-packages.mjs';

async function main() {
  const input = JSON.parse(await readStdin());
  const root = input.workspaceRoot;
  const issues = [];
  if (typeof root !== 'string' || !Array.isArray(input.workspace?.packages)) {
    issues.push({ check: 'release-build-graph', severity: 'error', message: 'kb-devkit did not provide the resolved workspace package and build-task graph', file: '.' });
  } else {
    const registryPath = path.join(root, 'e2e/platform/registry-manifest.json');
    try {
      const manifest = JSON.parse(readFileSync(registryPath, 'utf8'));
      for (const issue of releaseBuildGraphIssues(manifest, input.workspace.packages)) {
        issues.push({ check: 'release-build-graph', severity: 'error', message: issue.message, file: registryPath });
      }
    } catch (error) {
      issues.push({ check: 'release-build-graph', severity: 'error', message: `could not validate release packages against the devkit build graph: ${error.message}`, file: registryPath });
    }
  }
  process.stdout.write(JSON.stringify({ issues }));
}

if (process.argv[1] === fileURLToPath(import.meta.url)) await main();

export function releaseBuildGraphIssues(manifest, packages) {
  const byName = new Map(packages.map((pkg) => [pkg.name, pkg]));
  const issues = [];
  for (const name of releasePackageNames(manifest)) {
    const pkg = byName.get(name);
    if (!pkg) {
      issues.push({ message: `${name} is selected by the release registry but is absent from devkit workspace discovery; add its package category to devkit.yaml, then rerun \`kb-devkit check\`.` });
    } else if (!pkg.buildTarget) {
      issues.push({ message: `${name} is selected by the release registry but category ${pkg.category} has no build task; add a build task variant in devkit.yaml, then rerun \`kb-devkit check\`.` });
    }
  }
  return issues;
}

function readStdin() {
  return new Promise((resolve, reject) => {
    const chunks = [];
    process.stdin.setEncoding('utf8');
    process.stdin.on('data', (chunk) => chunks.push(chunk));
    process.stdin.on('end', () => resolve(chunks.join('')));
    process.stdin.on('error', reject);
  });
}
