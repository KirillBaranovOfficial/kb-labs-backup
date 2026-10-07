import assert from 'node:assert/strict';
import test from 'node:test';
import { buildCommand, failureMessage, validatePackageContents } from './validate-package-outputs.mjs';

test('requires declared build outputs and every main/export target to be packed', () => {
  const manifest = {
    name: '@kb-labs/example',
    files: ['dist', 'README.md'],
    main: './dist/index.js',
    exports: { '.': { types: './dist/index.d.ts', import: './dist/index.js' } },
  };
  const failures = validatePackageContents(manifest, [
    'package/package.json', 'package/README.md', 'package/dist/index.js',
  ]);
  assert.deepEqual(failures, [
    { field: 'exports', target: './dist/index.d.ts' },
  ]);
});

test('requires a package files build directory to be present in the archive', () => {
  const failures = validatePackageContents(
    { name: '@kb-labs/example', files: ['dist'] },
    ['package/package.json', 'package/README.md'],
  );
  assert.deepEqual(failures, [{ field: 'files', target: 'dist' }]);
});

test('requires Studio to pack dist/index.html and reports its exact dependency build command', () => {
  const manifest = { name: '@kb-labs/studio-app', files: ['dist'], main: './dist/index.js' };
  const failures = validatePackageContents(manifest, [
    'package/package.json', 'package/dist/index.js',
  ]);
  assert.deepEqual(failures, [{ field: 'Studio', target: './dist/index.html' }]);
  assert.equal(
    buildCommand(manifest.name),
    'corepack pnpm -r --filter "@kb-labs/studio-app..." --workspace-concurrency=2 build',
  );
  assert.equal(
    failureMessage(manifest, '/tmp/kb-labs-studio-app-2.0.0.tgz', failures[0]),
    'ERROR: @kb-labs/studio-app (kb-labs-studio-app-2.0.0.tgz) is missing packed Studio output ./dist/index.html. ' +
      'Build it with: corepack pnpm -r --filter "@kb-labs/studio-app..." --workspace-concurrency=2 build',
  );
});

test('matches declared build-output globs against the packed paths', () => {
  const failures = validatePackageContents(
    { name: '@kb-labs/example', files: ['dist/**/*.js'] },
    ['package/package.json', 'package/dist/commands/run.js'],
  );
  assert.deepEqual(failures, []);
});
