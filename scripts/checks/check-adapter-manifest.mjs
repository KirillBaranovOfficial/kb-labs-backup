#!/usr/bin/env node
import { readFile } from 'node:fs/promises';
import path from 'node:path';

const input = JSON.parse(await readStdin());
const checkName = input.check;
const items = [];

function issue(packageName, file, message) {
  items.push({
    check: checkName,
    severity: 'error',
    message: `${packageName}: ${message}`,
    file,
  });
}

async function main() {
  const workspace = input.workspace;
  if (!workspace || !Array.isArray(workspace.packages)) {
    throw new Error('workspace-scoped adapter-manifest check requires devkit workspace package data on stdin');
  }
  const platform = await import('@kb-labs/core-platform');

  const platformAdapters = [];
  for (const pkg of workspace.packages) {
    const result = await platform.loadAdapterManifest({ packageRoot: pkg.dir });
    const loaded = result.kind === 'adapter'
      ? { manifest: result.manifest, modulePath: result.modulePath }
      : { modulePath: result.modulePath, error: result.reason };
    if (pkg.category === 'adapter') {
      if (loaded.error) {
        issue(pkg.name, loaded.modulePath ?? path.join(pkg.dir, 'package.json'), `cannot load adapter manifest: ${loaded.error}`);
      } else if (!loaded.manifest.configSchema || typeof loaded.manifest.configSchema !== 'object' || Array.isArray(loaded.manifest.configSchema)) {
        issue(pkg.name, loaded.modulePath, 'adapter manifest must declare configSchema as an object (use {} when there are no options)');
      }
    }
    if (result.kind === 'adapter') {
      platformAdapters.push({ pkg, loaded });
    }
  }

  const categorizedAdapters = workspace.packages.filter(pkg => pkg.category === 'adapter');
  const discoveredPaths = new Set(platformAdapters.map(({ pkg }) => pkg.dir));
  for (const pkg of platformAdapters.map(({ pkg }) => pkg)) {
    if (pkg.category !== 'adapter') {
      issue(pkg.name, path.join(pkg.dir, 'package.json'), 'platform adapter manifest is outside the devkit adapter category');
    }
  }
  for (const pkg of categorizedAdapters) {
    if (!discoveredPaths.has(pkg.dir)) {
      issue(pkg.name, path.join(pkg.dir, 'package.json'), 'adapter category package is not discoverable by platform adapter discovery (built manifest must export implements)');
    }
  }

  process.stdout.write(JSON.stringify({ issues: items }));
}

function readStdin() {
  return new Promise((resolve, reject) => {
    const chunks = [];
    process.stdin.setEncoding('utf8');
    process.stdin.on('data', chunk => chunks.push(chunk));
    process.stdin.on('end', () => resolve(chunks.join('')));
    process.stdin.on('error', reject);
  });
}

await main();
