#!/usr/bin/env node

import { execFileSync } from 'node:child_process';
import { readdirSync } from 'node:fs';
import { basename, join } from 'node:path';
import { fileURLToPath } from 'node:url';

export function buildCommand(packageName) {
  return `corepack pnpm -r --filter "${packageName}..." --workspace-concurrency=2 build`;
}

export function failureMessage(manifest, tarball, failure) {
  return `ERROR: ${manifest.name} (${basename(tarball)}) is missing packed ${failure.field} output ${failure.target}. ` +
    `Build it with: ${buildCommand(manifest.name)}`;
}

function matchesTarget(files, target) {
  const normalized = target.replace(/^\.\//, '').replace(/\/$/, '');
  if (normalized.includes('*') || normalized.includes('?') || normalized.includes('[')) {
    let expression = '^';
    for (let index = 0; index < normalized.length; index += 1) {
      const character = normalized[index];
      if (character === '*' && normalized[index + 1] === '*') {
        index += 1;
        if (normalized[index + 1] === '/') {
          index += 1;
          expression += '(?:.*/)?';
        } else {
          expression += '.*';
        }
      } else if (character === '*') expression += '[^/]*';
      else if (character === '?') expression += '[^/]';
      else if (character === '[') {
        const end = normalized.indexOf(']', index + 1);
        if (end < 0) expression += '\\[';
        else {
          expression += normalized.slice(index, end + 1);
          index = end;
        }
      } else expression += character.replace(/[|\\{}()[\]^$+?.]/g, '\\$&');
    }
    const matcher = new RegExp(`${expression}$`);
    return files.some((file) => matcher.test(file));
  }
  return files.some((file) => file === normalized || file.startsWith(`${normalized}/`));
}

function exportTargets(value, targets = []) {
  if (typeof value === 'string' && value.startsWith('./')) targets.push(value);
  else if (Array.isArray(value)) value.forEach((entry) => exportTargets(entry, targets));
  else if (value && typeof value === 'object') Object.values(value).forEach((entry) => exportTargets(entry, targets));
  return targets;
}

export function validatePackageContents(manifest, packedPaths) {
  const files = packedPaths.map((file) => file.startsWith('package/') ? file.slice('package/'.length) : file);
  const failures = [];
  const check = (field, target) => {
    if (typeof target === 'string' && target && !matchesTarget(files, target)) {
      failures.push({ field, target });
    }
  };

  // `files` is an npm inclusion filter; absent optional source files such as
  // LICENSE are legal. Validate its build-output entries, which are the paths
  // that can be missing because a package was not built.
  for (const pattern of Array.isArray(manifest.files) ? manifest.files : []) {
    if (/^(?:dist|build|lib|out)(?:\/|$)/.test(pattern)) check('files', pattern);
  }
  check('main', manifest.main);
  exportTargets(manifest.exports).forEach((target) => check('exports', target));
  if (manifest.name === '@kb-labs/studio-app') check('Studio', './dist/index.html');
  return failures;
}

export function validateTarball(tarball) {
  const packageJson = execFileSync('tar', ['-xOzf', tarball, 'package/package.json'], { encoding: 'utf8' });
  const manifest = JSON.parse(packageJson);
  const packedPaths = execFileSync('tar', ['-tzf', tarball], { encoding: 'utf8' }).split(/\r?\n/).filter(Boolean);
  return { manifest, failures: validatePackageContents(manifest, packedPaths) };
}

function main() {
  const directory = process.argv[2];
  if (!directory) {
    process.stderr.write('Usage: validate-package-outputs.mjs <package-directory>\n');
    return 2;
  }
  const tarballs = readdirSync(directory).filter((filename) => filename.endsWith('.tgz'))
    .map((filename) => join(directory, filename)).sort();
  if (tarballs.length === 0) {
    process.stderr.write(`ERROR: no package tarballs found in ${directory}\n`);
    return 1;
  }
  let invalid = 0;
  for (const tarball of tarballs) {
    const { manifest, failures } = validateTarball(tarball);
    for (const failure of failures) {
      invalid += 1;
      process.stderr.write(`${failureMessage(manifest, tarball, failure)}\n`);
    }
  }
  if (invalid > 0) return 1;
  process.stdout.write(`Validated declared package outputs in ${tarballs.length} package tarballs\n`);
  return 0;
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  try {
    process.exitCode = main();
  } catch (error) {
    process.stderr.write(`ERROR: package output validation failed: ${error.message}\n`);
    process.exitCode = 1;
  }
}
