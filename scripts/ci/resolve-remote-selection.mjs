#!/usr/bin/env node
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const verificationProfile = JSON.parse(readFileSync(new URL('./verification-profile.json', import.meta.url), 'utf8'));
const profile = process.argv[2];
const outputPath = process.argv[3];
const executable = process.argv[4];
const defaultBranch = process.env.DEFAULT_BRANCH;
if (!['stream', 'candidate'].includes(profile) || !outputPath || !executable || (profile === 'stream' && !defaultBranch)) {
  throw new Error('A supported profile, output path, and kb-devkit executable are required; stream selection also requires DEFAULT_BRANCH.');
}
let baseRef;
if (profile === 'stream') {
  baseRef = `origin/${defaultBranch}...HEAD`;
} else {
  const emptyTree = spawnSync('git', ['hash-object', '-t', 'tree', '--stdin'], { cwd: root, input: '', encoding: 'utf8' });
  if (emptyTree.error || emptyTree.status !== 0) {
    throw new Error(`Could not resolve the candidate empty-tree base ref; run git hash-object -t tree --stdin. ${emptyTree.error?.message || emptyTree.stderr}`);
  }
  baseRef = emptyTree.stdout.trim();
}

const temporaryRoot = mkdtempSync(path.join(os.tmpdir(), 'kb-remote-selection-'));
const changedFilesPath = path.join(temporaryRoot, 'changed-files.json');
try {
  const changed = spawnSync(process.execPath, ['scripts/ci/remote-changed-files.mjs'], {
    cwd: root,
    encoding: 'utf8',
    env: { ...process.env, VERIFICATION_PROFILE: profile, KB_DEVKIT_BASE_REF: baseRef, KB_REMOTE_CHANGED_FILES_PATH: changedFilesPath },
  });
  if (changed.error) throw changed.error;
  if (changed.status !== 0) throw new Error(`Remote changed-path selection failed (exit ${changed.status}): ${changed.stderr || changed.stdout}`);
  const result = spawnSync(executable, ['--config', path.join(root, 'devkit.yaml'), 'affected', '--changed-files-file', changedFilesPath, '--json'], {
    cwd: root,
    encoding: 'utf8',
    env: { ...process.env, VERIFICATION_PROFILE: profile, KB_DEVKIT_BASE_REF: baseRef, KB_REMOTE_CHANGED_FILES_PATH: changedFilesPath },
  });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`kb-devkit affected selection failed (exit ${result.status}): ${result.stderr || result.stdout}`);
  const envelope = JSON.parse(result.stdout);
  const packages = envelope?.data?.packages;
  const internalToolPackages = envelope?.data?.internalToolPackages;
  const selectionReason = envelope?.data?.selectionReason;
  if (envelope?.ok !== true || !Array.isArray(packages) || packages.some((name) => typeof name !== 'string' || !name)) {
    throw new Error('kb-devkit affected --json returned an invalid package selection. Rerun the remote verification prepare job.');
  }
  if (typeof selectionReason !== 'string') throw new Error('kb-devkit affected --json returned an invalid selection reason. Rerun the remote verification prepare job.');
  if (!Array.isArray(internalToolPackages)) throw new Error('kb-devkit affected --json returned no internal-tool package selection. Rerun the remote verification prepare job.');
  const stages = [...verificationProfile.independentTestStages[profile], ...verificationProfile.buildDependentTestStages[profile]];
  const stagePackages = {};
  for (const stage of stages) {
    const definition = verificationProfile.stages[stage];
    if (definition.runner !== 'kb-devkit-test-package') continue;
    if (typeof definition.packageManifest !== 'string' || !definition.packageManifest) throw new Error(`Verification stage ${stage} needs a registered package manifest.`);
    const manifest = JSON.parse(readFileSync(path.join(root, definition.packageManifest), 'utf8'));
    if (typeof manifest.name !== 'string' || !manifest.name) throw new Error(`Verification stage ${stage} package manifest ${definition.packageManifest} has no package name.`);
    stagePackages[stage] = [manifest.name];
  }
  const changedPaths = JSON.parse(readFileSync(changedFilesPath, 'utf8'));
  writeFileSync(outputPath, `${JSON.stringify({ ok: true, data: { packages, internalToolPackages, stagePackages, changedPathCount: changedPaths.length, selectionReason }, selectedPackageCount: packages.length })}\n`);
  process.stdout.write(`Resolved ${packages.length} package(s) from ${changedPaths.length} path(s) using kb-devkit.\n`);
} catch (error) {
  process.stderr.write(`${error.message}\n`);
  process.exitCode = 1;
} finally {
  rmSync(temporaryRoot, { recursive: true, force: true });
}
