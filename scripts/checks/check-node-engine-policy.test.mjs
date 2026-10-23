import assert from 'node:assert/strict';
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { readFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { parseNodeEnginePolicy, readNodeEngineDeclaration } from '../../core/plugin-runtime/src/platform-resolve/node-engine.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const checker = path.join(root, 'scripts/checks/check-node-engine-policy.mjs');
const generator = path.join(root, 'tools/clikit/toolchain/generate-node-policy.mjs');
const devkitModule = path.join(root, 'tools/kb-devkit');
const realDockerfileStatusBeforeTests = dockerfileWorktreeStatus();

// case: S-260.2
test('the shipped Node policy passes the workspace check', () => {
  const result = runDevkitCheck();

  assert.equal(result.status, 0, result.stderr);
  assert.equal(JSON.parse(result.stdout).ok, true, result.stdout);
});

test('node-engine-policy check diagnoses missing external-check input', () => {
  const result = spawnSync(process.execPath, [checker], {
    cwd: root,
    encoding: 'utf8',
  });

  assert.equal(result.status, 2);
  assert.match(result.stderr, /requires workspace input on stdin; next: kb-devkit check --only node-engine-policy --json/);
  assert.doesNotMatch(result.stderr, /SyntaxError|Unexpected end of JSON/);
});

test('workspace engine policy names every package whose declaration differs', async () => {
  const workspaceRoot = await fixtureWorkspace('24.x');
  try {
    const generated = spawnSync(process.execPath, [generator, '--root', workspaceRoot], { encoding: 'utf8' });
    assert.equal(generated.status, 0, generated.stderr);
    await writePackage(workspaceRoot, 'packages/matching', '@fixture/matching', '24.x');
    await writePackage(workspaceRoot, 'packages/mismatch', '@fixture/mismatch', '26.x');
    const result = runCheck(workspaceRoot, [
      packageRecord(workspaceRoot, 'packages/matching', '@fixture/matching'),
      packageRecord(workspaceRoot, 'packages/mismatch', '@fixture/mismatch'),
    ]);

    assert.equal(result.status, 0, result.stderr);
    const issues = JSON.parse(result.stdout).issues;
    assert.equal(issues.length, 1);
    assert.match(issues[0].message, /@fixture\/mismatch/);
    assert.match(issues[0].message, /26\.x/);
    assert.equal(issues[0].file, path.join(workspaceRoot, 'packages/mismatch/package.json'));
  } finally {
    await rm(workspaceRoot, { recursive: true, force: true });
  }
});

test('workspace engine policy rejects any root syntax other than MAJOR.x', async () => {
  const workspaceRoot = await fixtureWorkspace('^24.x');
  try {
    await writePackage(workspaceRoot, 'packages/declared', '@fixture/declared', '^24.x');
    const result = runCheck(workspaceRoot, [packageRecord(workspaceRoot, 'packages/declared', '@fixture/declared')]);

    assert.equal(result.status, 0, result.stderr);
    const issues = JSON.parse(result.stdout).issues;
    assert.equal(issues.length, 1);
    assert.match(issues[0].message, /Unsupported Node\.js engine declaration "\^24\.x"/);
    assert.equal(issues[0].file, path.join(workspaceRoot, 'package.json'));
  } finally {
    await rm(workspaceRoot, { recursive: true, force: true });
  }
});

// case: S-260.3
test('generated Go Node policy matches the owner and rejects root-owner drift', async () => {
  const workspaceRoot = await fixtureWorkspace('24.x');
  try {
    await writePackage(workspaceRoot, 'packages/declared', '@fixture/declared', '24.x');
    const generatedResult = spawnSync(process.execPath, [generator, '--root', workspaceRoot], { encoding: 'utf8' });
    assert.equal(generatedResult.status, 0, generatedResult.stderr);

    const rootManifestPath = path.join(workspaceRoot, 'package.json');
    const rootManifest = JSON.parse(await readFile(rootManifestPath, 'utf8'));
    rootManifest.engines.node = '26.x';
    await writeFile(rootManifestPath, JSON.stringify(rootManifest));
    const packageManifestPath = path.join(workspaceRoot, 'packages/declared/package.json');
    const packageManifest = JSON.parse(await readFile(packageManifestPath, 'utf8'));
    packageManifest.engines.node = '26.x';
    await writeFile(packageManifestPath, JSON.stringify(packageManifest));
    const ownerDrift = runCheck(workspaceRoot, [packageRecord(workspaceRoot, 'packages/declared', '@fixture/declared')]);
    assert.equal(ownerDrift.status, 0, ownerDrift.stderr);
    assert.match(JSON.parse(ownerDrift.stdout).issues.map(({ message }) => message).join('\n'), /generated Go Node policy is stale/);

  } finally {
    await rm(workspaceRoot, { recursive: true, force: true });
  }
});

test('Go compilation rejects a duplicate generated node policy constant in an isolated package', async () => {
  const fixtureRoot = await mkdtemp(path.join(os.tmpdir(), 'kb-node-policy-duplicate-'));
  try {
    const generatedSource = await readFile(path.join(root, 'tools/clikit/toolchain/node_policy_gen.go'), 'utf8');
    await writeFile(path.join(fixtureRoot, 'go.mod'), 'module fixture\n\ngo 1.22\n');
    await writeFile(path.join(fixtureRoot, 'node_policy_gen.go'), generatedSource);
    await writeFile(path.join(fixtureRoot, 'handwritten.go'), generatedSource);
    const result = spawnSync('go', ['test', './...'], { cwd: fixtureRoot, encoding: 'utf8' });

    assert.notEqual(result.status, 0);
    assert.match(result.stdout + result.stderr, /SupportedNodeMajor redeclared|already declared/);
  } finally {
    await rm(fixtureRoot, { recursive: true, force: true });
  }
});

// case: S-260.5
test('tracked Node image with the matching policy tag passes', async (t) => {
  const declaration = readNodeEngineDeclaration(path.join(root, 'package.json'));
  const requiredMajor = parseNodeEnginePolicy(declaration).requiredMajor;
  const { repositoryRoot } = await createTrackedDockerfileRepo(t, `FROM node:${requiredMajor}-slim AS runner\n`);
  const result = runCheck(repositoryRoot, []);
  assert.deepEqual(JSON.parse(result.stdout).issues, []);
});

// case: S-260.5
test('tracked Node image with a mismatched tag fails with file and line', async (t) => {
  const declaration = readNodeEngineDeclaration(path.join(root, 'package.json'));
  const requiredMajor = parseNodeEnginePolicy(declaration).requiredMajor;
  const unsupportedMajor = requiredMajor + 2;
  const { repositoryRoot } = await createTrackedDockerfileRepo(t, `FROM node:${unsupportedMajor}-slim AS runner\n`);
  const issues = JSON.parse(runCheck(repositoryRoot, []).stdout).issues;

  assert.equal(issues.length, 1);
  assert.equal(issues[0].file, 'Dockerfile:1');
  assert.match(issues[0].message, new RegExp(`Node ${unsupportedMajor}`));
});

// case: S-260.5
test('tracked Node image with an unresolved ARG fails with file and line', async (t) => {
  const { repositoryRoot } = await createTrackedDockerfileRepo(t, 'FROM node:${X} AS runner\n');
  const issues = JSON.parse(runCheck(repositoryRoot, []).stdout).issues;

  assert.equal(issues.length, 1);
  assert.equal(issues[0].file, 'Dockerfile:1');
  assert.match(issues[0].message, /unresolved ARG or variable/);
});

// case: S-260.5
test('tracked Node image resolves a declared global ARG default', async (t) => {
  const declaration = readNodeEngineDeclaration(path.join(root, 'package.json'));
  const requiredMajor = parseNodeEnginePolicy(declaration).requiredMajor;
  const { repositoryRoot } = await createTrackedDockerfileRepo(
    t,
    `ARG NODE_MAJOR=${requiredMajor}\nFROM node:\${NODE_MAJOR}-slim \\\n  AS runner\n`,
  );
  assert.deepEqual(JSON.parse(runCheck(repositoryRoot, []).stdout).issues, []);
});

test('Dockerfile cases leave real-worktree Dockerfile status unchanged', () => {
  assert.equal(dockerfileWorktreeStatus(), realDockerfileStatusBeforeTests);
});

async function fixtureWorkspace(policy) {
  const workspaceRoot = await mkdtemp(path.join(os.tmpdir(), 'kb-node-engine-policy-'));
  await writePackage(workspaceRoot, '.', '@fixture/root', policy);
  await initializeTemporaryGit(workspaceRoot, ['package.json']);
  return workspaceRoot;
}

async function createTrackedDockerfileRepo(t, dockerfile) {
  const repositoryRoot = await mkdtemp(path.join(os.tmpdir(), 'kb-node-docker-policy-'));
  t.after(() => rm(repositoryRoot, { recursive: true, force: true }));
  const declaration = readNodeEngineDeclaration(path.join(root, 'package.json'));
  await writeFile(path.join(repositoryRoot, 'package.json'), JSON.stringify({ engines: { node: declaration } }));
  await writeFile(path.join(repositoryRoot, 'Dockerfile'), dockerfile);
  await initializeTemporaryGit(repositoryRoot, ['package.json', 'Dockerfile']);
  const generated = spawnSync(process.execPath, [generator, '--root', repositoryRoot], { encoding: 'utf8' });
  assert.equal(generated.status, 0, generated.stderr || generated.stdout);

  return {
    repositoryRoot,
    requiredMajor: parseNodeEnginePolicy(declaration).requiredMajor,
  };
}

async function initializeTemporaryGit(repositoryRoot, files) {
  for (const args of [['init', '--quiet'], ['add', '--', ...files]]) {
    const result = spawnSync('git', args, { cwd: repositoryRoot, encoding: 'utf8' });
    assert.equal(result.status, 0, result.stderr || result.stdout);
  }
}

function dockerfileWorktreeStatus() {
  const result = spawnSync('git', ['status', '--porcelain', '--untracked-files=all', '--', '*Dockerfile*'], {
    cwd: root,
    encoding: 'utf8',
  });
  assert.equal(result.status, 0, result.stderr || result.stdout);
  return result.stdout;
}

async function writePackage(workspaceRoot, relativePath, name, policy) {
  const packageRoot = path.resolve(workspaceRoot, relativePath);
  await mkdir(packageRoot, { recursive: true });
  await writeFile(path.join(packageRoot, 'package.json'), JSON.stringify({
    name,
    engines: { node: policy },
  }));
}

function packageRecord(workspaceRoot, relativePath, name) {
  return { name, dir: path.resolve(workspaceRoot, relativePath) };
}

function runCheck(workspaceRoot, packages) {
  return spawnSync(process.execPath, [checker], {
    cwd: root,
    input: JSON.stringify({
      check: 'node-engine-policy',
      workspaceRoot,
      workspace: { packages },
    }),
    encoding: 'utf8',
  });
}

function runDevkitCheck() {
  return spawnSync('go', ['-C', devkitModule, 'run', '.', 'check', '--only', 'node-engine-policy', '--json'], {
    cwd: root,
    encoding: 'utf8',
  });
}
