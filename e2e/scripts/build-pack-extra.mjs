#!/usr/bin/env node
import { execFileSync } from 'node:child_process';
import { cpSync, existsSync, readFileSync, mkdirSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const scriptDirectory = path.dirname(fileURLToPath(import.meta.url));
const repositoryRoot = path.resolve(scriptDirectory, '../..');
const packageDirectory = process.cwd();
const packageManifestPath = path.join(packageDirectory, 'package.json');
const packageManifest = JSON.parse(readFileSync(packageManifestPath, 'utf8'));
const extras = JSON.parse(readFileSync(path.join(scriptDirectory, 'pack-extras.json'), 'utf8'));
const relativeDirectory = path.relative(repositoryRoot, packageDirectory).split(path.sep).join('/');
if (!extras.some((extra) => extra.directory === relativeDirectory)) {
  process.stderr.write(`ERROR: ${packageManifest.name} is not declared in e2e/scripts/pack-extras.json; next: add its directory to that registry and run kb-devkit run build.\n`);
  process.exit(1);
}
const manifestModule = packageManifest.kb?.adapter?.manifest ?? packageManifest.kb?.manifest;
if (typeof manifestModule !== 'string' || !manifestModule.startsWith('./')) {
  process.stderr.write(`ERROR: ${packageManifest.name} must declare its KB manifest module in package.json before its candidate output can be built.\n`);
  process.exit(1);
}
const output = path.join(packageDirectory, 'dist/manifest.json');
mkdirSync(path.dirname(output), { recursive: true });
const sourceDirectory = path.join(packageDirectory, 'src');
if (existsSync(sourceDirectory)) cpSync(sourceDirectory, path.join(packageDirectory, 'dist'), { recursive: true, force: true });
const generator = path.join(repositoryRoot, 'infra/devkit/tsup/emit-manifest-json.mjs');
try {
  execFileSync(process.execPath, [generator, '--module', path.join(packageDirectory, manifestModule), '--output', output], { stdio: 'inherit' });
} catch (error) {
  const detail = Buffer.isBuffer(error.stderr) ? error.stderr.toString('utf8').trim() : error.message;
  process.stderr.write(`ERROR: could not build ${packageManifest.name} output dist/manifest.json: ${detail}; next: kb-devkit run build --package "${packageManifest.name}".\n`);
  process.exit(1);
}
