#!/usr/bin/env node

import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

const value = name => {
  const index = process.argv.indexOf(name);
  return index >= 0 ? process.argv[index + 1] : undefined;
};

const packagesDirectory = resolve(value('--packages-dir') ?? '');
if (!value('--packages-dir')) throw new Error('--packages-dir is required');
const packages = execFileSync('find', [packagesDirectory, '-maxdepth', '1', '-type', 'f', '-name', '*.tgz'],
  { encoding: 'utf8' }).trim().split('\n').filter(Boolean);
const sources = packages.filter(tarball => {
  const manifest = JSON.parse(execFileSync('tar', ['-xOzf', tarball, 'package/package.json'], { encoding: 'utf8' }));
  return manifest.name === '@kb-labs/commit-entry';
});
if (sources.length !== 1) throw new Error(`expected one packed Commit source artifact, found ${sources.length}`);

const temporaryDirectory = mkdtempSync(join(tmpdir(), 'kb-commit-update-fixture-'));
try {
  execFileSync('tar', ['-xzf', sources[0], '-C', temporaryDirectory]);
  const packageDirectory = join(temporaryDirectory, 'package');
  const npmEnvironment = {
    ...process.env,
    HOME: temporaryDirectory,
    npm_config_cache: join(temporaryDirectory, '.npm-cache'),
    npm_config_userconfig: join(temporaryDirectory, '.npmrc'),
  };
  execFileSync('npm', ['version', 'major', '--no-git-tag-version', '--ignore-scripts'], {
    cwd: packageDirectory,
    env: npmEnvironment,
    stdio: 'inherit',
  });
  const generated = execFileSync('npm', ['pack', '--pack-destination', packagesDirectory], {
    cwd: packageDirectory,
    env: npmEnvironment,
    encoding: 'utf8',
  }).trim();
  const candidate = join(packagesDirectory, generated.split('\n').at(-1));
  const candidateManifest = JSON.parse(execFileSync('tar', ['-xOzf', candidate, 'package/package.json'], { encoding: 'utf8' }));
  const sourceManifest = JSON.parse(execFileSync('tar', ['-xOzf', sources[0], 'package/package.json'], { encoding: 'utf8' }));
  if (candidateManifest.name !== sourceManifest.name || candidateManifest.version === sourceManifest.version) {
    throw new Error('generated Commit update artifact did not advance its source package version');
  }
} finally {
  rmSync(temporaryDirectory, { recursive: true, force: true });
}
