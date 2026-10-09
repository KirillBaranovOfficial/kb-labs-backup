import assert from 'node:assert/strict';
import test from 'node:test';
import { buildCommand, failureMessage, validatePackageContents, validatePackedSource } from './validate-package-outputs.mjs';

test('rejects a packed manifest that diverges from the source package manifest', () => {
  const source = {
    name: '@kb-labs/qa-entry',
    version: '2.120.1',
    dependencies: { '@kb-labs/sdk': 'workspace:*' },
  };
  const packed = {
    name: '@kb-labs/qa-entry',
    version: '2.120.1',
    dependencies: { '@kb-labs/sdk': '2.120.1' },
  };
  const errors = validatePackedSource(source, packed, {
    sourcePackedPackage: packed,
    sourceManifest: { commands: [{ flags: ['run-id', 'exclude-job'] }] },
    packedManifest: { commands: [{ flags: ['runId', 'excludeJob'] }] },
    workspacePackageNames: new Set(['@kb-labs/qa-entry', '@kb-labs/sdk']),
    candidateNames: new Set(['@kb-labs/qa-entry', '@kb-labs/sdk']),
  });

  assert.deepEqual(errors, [
    'dist/manifest.json differs from the workspace build output',
  ]);
});

test('rejects a packed dependency closure pinned to a different workspace version', () => {
  const source = {
    name: '@kb-labs/mind-entry',
    version: '2.120.1',
    dependencies: { '@kb-labs/sdk': 'workspace:*' },
  };
  const packed = {
    name: '@kb-labs/mind-entry',
    version: '2.120.1',
    dependencies: { '@kb-labs/sdk': '2.117.0' },
  };

  assert.deepEqual(validatePackedSource(source, packed, {
    sourcePackedPackage: { ...packed, dependencies: { '@kb-labs/sdk': '2.120.1' } },
    workspacePackageNames: new Set(['@kb-labs/mind-entry', '@kb-labs/sdk']),
    candidateNames: new Set(['@kb-labs/mind-entry', '@kb-labs/sdk']),
  }), [
    'package.json differs from the package freshly packed from workspace source',
  ]);
});

test('rejects a candidate missing an internal package required by a packed dependency', () => {
  const packageSource = {
    name: '@kb-labs/mind-entry',
    version: '2.120.1',
    dependencies: { '@kb-labs/sdk': 'workspace:*' },
  };
  assert.deepEqual(validatePackedSource(packageSource, {
    name: '@kb-labs/mind-entry',
    version: '2.120.1',
    dependencies: { '@kb-labs/sdk': '2.120.1' },
  }, {
    sourcePackedPackage: {
      name: '@kb-labs/mind-entry',
      version: '2.120.1',
      dependencies: { '@kb-labs/sdk': '2.120.1' },
    },
    workspacePackageNames: new Set(['@kb-labs/mind-entry', '@kb-labs/sdk']),
    candidateNames: new Set(['@kb-labs/mind-entry']),
  }), [
    'dependencies.@kb-labs/sdk is required from the workspace but missing from the candidate package set',
  ]);
});

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
