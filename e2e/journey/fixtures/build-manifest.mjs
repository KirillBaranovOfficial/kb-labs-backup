import { execFileSync } from 'node:child_process';
import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';

const packageDirectory = resolve(process.argv[2] ?? '');
if (!process.argv[2]) throw new Error('fixture package directory is required');
const workspaceRoot = resolve(packageDirectory, '../../../..');
const outputDirectory = resolve(packageDirectory, 'dist');
await mkdir(outputDirectory, { recursive: true });
execFileSync(process.execPath, [
  resolve(workspaceRoot, 'infra/devkit/tsup/emit-manifest-json.mjs'),
  '--module', resolve(packageDirectory, 'manifest.js'),
  '--output', resolve(outputDirectory, 'manifest.json'),
], { stdio: 'inherit' });
