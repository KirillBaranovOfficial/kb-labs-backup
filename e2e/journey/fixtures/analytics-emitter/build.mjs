import { cp, mkdir } from 'node:fs/promises';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

const directory = dirname(fileURLToPath(import.meta.url));
const workspaceRoot = resolve(directory, '../../../..');
const outputDirectory = resolve(directory, 'dist');
await mkdir(outputDirectory, { recursive: true });
await cp(resolve(directory, 'src/commands'), resolve(outputDirectory, 'commands'), { recursive: true });
execFileSync(process.execPath, [
  resolve(workspaceRoot, 'infra/devkit/tsup/emit-manifest-json.mjs'),
  '--module', resolve(directory, 'manifest.js'),
  '--output', resolve(outputDirectory, 'manifest.json'),
], { stdio: 'inherit' });
