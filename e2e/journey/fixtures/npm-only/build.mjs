import { execFileSync } from 'node:child_process';
import { mkdir } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const directory = dirname(fileURLToPath(import.meta.url));
const workspaceRoot = resolve(directory, '../../../..');
const output = resolve(directory, 'dist/manifest.json');
await mkdir(dirname(output), { recursive: true });
execFileSync(process.execPath, [
  resolve(workspaceRoot, 'infra/devkit/tsup/emit-manifest-json.mjs'),
  '--module', resolve(directory, 'manifest.js'), '--output', output,
], { stdio: 'inherit' });
