import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, rmSync, symlinkSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const deliveryDirectory = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const repositoryRoot = path.resolve(deliveryDirectory, '../..');
const composeFile = path.join(deliveryDirectory, 'docker-compose.yml');
const invoker = path.join(deliveryDirectory, 'scripts/run.sh');
const composeResolver = path.join(deliveryDirectory, 'scripts/resolve-compose.sh');
const policyCli = path.join(repositoryRoot, 'core/plugin-runtime/src/platform-resolve/node-engine.mjs');
const rootManifest = path.join(repositoryRoot, 'package.json');

// case: S-260.5
test('delivery compose config rejects an unset Node policy argument', () => {
  const resolved = spawnSync(composeResolver, [], { encoding: 'utf8' });
  assert.equal(resolved.status, 0, resolved.stderr);
  const composeBin = resolved.stdout.trim();
  const environment = { ...process.env };
  delete environment.KB_NODE_MAJOR;
  const result = spawnSync(composeBin, ['-f', composeFile, 'config', '--format', 'json'], {
    cwd: deliveryDirectory,
    env: environment,
    encoding: 'utf8',
  });

  assert.notEqual(result.status, 0, result.stdout);
  assert.match(`${result.stdout}\n${result.stderr}`, /KB_NODE_MAJOR/);
  assert.match(`${result.stdout}\n${result.stderr}`, /root package\.json engines\.node/);
});

// case: S-260.5
test('delivery invoker renders the root Node major to both targets', () => {
  const policy = spawnSync(process.execPath, [policyCli, '--required-major', rootManifest], { encoding: 'utf8' });
  assert.equal(policy.status, 0, policy.stderr);
  const requiredMajor = Number(policy.stdout.trim());

  const environment = { ...process.env };
  delete environment.KB_NODE_MAJOR;
  const result = spawnSync(invoker, ['config', '--format', 'json'], {
    cwd: deliveryDirectory,
    env: environment,
    encoding: 'utf8',
  });
  assert.equal(result.status, 0, result.stderr || result.error);

  const config = JSON.parse(result.stdout);
  assert.equal(config.services['target-1'].build.args.NODE_MAJOR, String(requiredMajor));
  assert.equal(config.services['target-2'].build.args.NODE_MAJOR, String(requiredMajor));
});

// case: S-260.5
test('delivery invoker reports missing docker-compose on PATH', (context) => {
  const tempDirectory = mkdtempSync(path.join(os.tmpdir(), 'kb-delivery-no-compose-'));
  context.after(() => rmSync(tempDirectory, { recursive: true, force: true }));
  const binDirectory = path.join(tempDirectory, 'bin');
  mkdirSync(binDirectory);
  symlinkSync(process.execPath, path.join(binDirectory, 'node'));
  symlinkSync('/bin/bash', path.join(binDirectory, 'bash'));
  symlinkSync('/usr/bin/dirname', path.join(binDirectory, 'dirname'));

  const environment = { ...process.env, PATH: binDirectory };
  delete environment.KB_NODE_MAJOR;
  delete environment.KB_DELIVERY_COMPOSE_BIN;
  const result = spawnSync(invoker, ['config', '--format', 'json'], {
    cwd: deliveryDirectory,
    env: environment,
    encoding: 'utf8',
  });

  assert.equal(result.status, 127, `${result.stdout}\n${result.stderr}`);
  assert.match(result.stderr, /Required tool docker-compose was not found on PATH/);
  assert.match(result.stderr, /run e2e\/delivery\/scripts\/run\.sh from the repository root/);
});
