import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync, realpathSync } from 'node:fs';
import { delimiter, dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const nodePolicyPattern = /^(0|[1-9]\d*)\.x$/;

export function parseNodeEnginePolicy(value) {
  if (typeof value !== 'string') {
    throw new Error('The platform must declare engines.node as MAJOR.x.');
  }
  const match = nodePolicyPattern.exec(value);
  if (!match) {
    throw new Error(`Unsupported Node.js engine declaration "${value}"; use exactly MAJOR.x.`);
  }
  return { requiredMajor: Number(match[1]) };
}

export function nodeVersionSupported(requiredMajor, actualVersion) {
  const actualMajor = parseNodeMajor(actualVersion);
  return actualMajor === Number(requiredMajor);
}

function parseNodeMajor(version) {
  if (typeof version !== 'string') throw new Error(`Invalid Node.js version "${version}".`);
  const majorText = version.trim().replace(/^v/, '').split('.')[0];
  const major = Number(majorText);
  if (!Number.isSafeInteger(major) || major < 0 || majorText === '') {
    throw new Error(`Invalid Node.js version "${version}".`);
  }
  return major;
}

export function readNodeEnginePolicy(packageManifest) {
  const parsed = readManifest(packageManifest);
  if (parsed.name !== '@kb-labs/plugin-runtime') {
    throw new Error(`Runtime engine manifest ${packageManifest} declares ${JSON.stringify(parsed.name)}.`);
  }
  return parseNodeEnginePolicy(parsed.engines?.node);
}

export function readNodeEngineDeclaration(packageManifest) {
  const parsed = readManifest(packageManifest);
  const policy = parsed.engines?.node;
  if (policy === undefined) return undefined;
  if (typeof policy !== 'string') {
    throw new Error(`Node.js engine declaration in ${packageManifest} must be a string.`);
  }
  return policy;
}

function readManifest(packageManifest) {
  try {
    return JSON.parse(readFileSync(packageManifest, 'utf8'));
  } catch (error) {
    throw new Error(`Cannot read package manifest ${packageManifest}: ${error.message}`);
  }
}

export function runtimePackageManifest(moduleURL) {
  let directory = dirname(fileURLToPath(moduleURL));
  for (;;) {
    const manifest = join(directory, 'package.json');
    if (existsSync(manifest)) {
      const parsed = JSON.parse(readFileSync(manifest, 'utf8'));
      if (parsed.name === '@kb-labs/plugin-runtime') return manifest;
    }
    const parent = dirname(directory);
    if (parent === directory) break;
    directory = parent;
  }
  throw new Error(`Cannot locate the installed @kb-labs/plugin-runtime/package.json from ${moduleURL}.`);
}

export function selectNodeExecutable(rootManifest, environment = process.env) {
  const declaration = readNodeEngineDeclaration(rootManifest);
  const { requiredMajor } = parseNodeEnginePolicy(declaration);
  const candidates = new Set((environment.PATH ?? '').split(delimiter).map((directory) => join(directory || '.', 'node')));
  const rejected = [];
  for (const candidate of candidates) {
    if (!existsSync(candidate)) continue;
    let version;
    try {
      version = execFileSync(candidate, ['--version'], { encoding: 'utf8', timeout: 5000 }).trim();
    } catch (error) {
      rejected.push(`${candidate} (could not read version: ${error.message})`);
      continue;
    }
    if (nodeVersionSupported(requiredMajor, version)) return candidate;
    rejected.push(`${candidate} (${version})`);
  }
  const checked = rejected.length > 0 ? rejected.join(', ') : 'no Node.js executables were found on PATH';
  throw new Error(`No installed Node.js major matches ${requiredMajor}.x; checked ${checked}. Install Node.js ${requiredMajor}, then rerun this command.`);
}

if (process.argv[1] && realpathSync(process.argv[1]) === fileURLToPath(import.meta.url)) {
  if (process.argv.length !== 4 || !['--select', '--required-major'].includes(process.argv[2])) {
    process.stderr.write('Usage: node node-engine.mjs (--select|--required-major) <root-package.json>\n');
    process.exitCode = 2;
  } else if (process.argv[2] === '--required-major') {
    try {
      const declaration = readNodeEngineDeclaration(process.argv[3]);
      process.stdout.write(`${parseNodeEnginePolicy(declaration).requiredMajor}\n`);
    } catch (error) {
      process.stderr.write(`${error.message}\n`);
      process.exitCode = 2;
    }
  } else {
    try {
      process.stdout.write(`${selectNodeExecutable(process.argv[3])}\n`);
    } catch (error) {
      process.stderr.write(`${error.message}\n`);
      process.exitCode = 2;
    }
  }
}
