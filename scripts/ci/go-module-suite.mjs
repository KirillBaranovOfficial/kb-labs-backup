#!/usr/bin/env node
import { spawnSync } from 'node:child_process';
import { globSync, mkdtempSync, mkdirSync, rmSync, statSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const workspaceRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const registryReader = path.join(workspaceRoot, 'scripts/read-go-binary-registry.mjs');

function usage() {
  return 'Usage: node scripts/ci/go-module-suite.mjs [--registry <devkit.yaml>] [--root <workspace>] [--go <go-command>] [--module <path> ...]';
}

function parseArgs(args) {
  const options = { root: workspaceRoot, go: 'go', modules: [] };
  for (let index = 0; index < args.length; index += 1) {
    const flag = args[index];
    if (flag === '--help') return { help: true };
    if (!['--registry', '--root', '--go', '--module'].includes(flag) || !args[index + 1]) {
      throw new Error(`${usage()}\nInvalid or incomplete argument: ${flag}`);
    }
    const value = args[++index];
    if (flag === '--module') options.modules.push(value);
    else options[flag.slice(2)] = value;
  }
  return options;
}

function discoverModules(root, registryPath) {
  const result = spawnSync(process.execPath, [registryReader, '--config', registryPath], {
    cwd: root,
    encoding: 'utf8',
  });
  if (result.status !== 0) throw new Error(`Go module registry discovery failed: ${result.stderr.trim() || result.stdout.trim()}`);
  let patterns;
  try {
    patterns = JSON.parse(result.stdout);
  } catch (error) {
    throw new Error(`Go module registry discovery returned invalid JSON: ${error.message}`);
  }
  if (!Array.isArray(patterns) || patterns.length === 0) throw new Error('Go module registry discovery returned an empty set');

  const modules = new Set();
  for (const pattern of patterns) {
    const matches = globSync(pattern, { cwd: root, withFileTypes: true });
    const directories = matches
      .filter((entry) => entry.isDirectory())
      .map((entry) => path.join(entry.parentPath, entry.name))
      .filter((directory) => {
        try { return statSync(path.join(directory, 'go.mod')).isFile(); }
        catch (error) { if (error.code === 'ENOENT') return false; throw error; }
      });
    if (directories.length === 0) throw new Error(`Go module registry pattern matched no modules: ${pattern}`);
    for (const directory of directories) modules.add(path.relative(root, directory));
  }
  if (modules.size === 0) throw new Error('Go module registry discovery returned an empty set');
  return [...modules].sort();
}

function run(command, args, options) {
  const result = spawnSync(command, args, { encoding: 'utf8', ...options });
  if (result.error) throw result.error;
  return result;
}

function goPackages(go, moduleDir, env) {
  const result = run(go, ['list', './...'], { cwd: moduleDir, env });
  if (result.status !== 0) throw new Error(`go list failed in ${path.relative(workspaceRoot, moduleDir)}: ${result.stderr.trim()}`);
  const packages = result.stdout.split(/\r?\n/).filter(Boolean).filter((name) => !name.split('/').includes('e2e'));
  if (packages.length === 0) throw new Error(`No non-e2e Go packages found in ${path.relative(workspaceRoot, moduleDir)}`);
  return packages;
}

function runSuite({ root = workspaceRoot, registry, go = 'go', modules = [] } = {}) {
  const registryPath = registry ?? path.join(root, 'devkit.yaml');
  const discovered = discoverModules(root, registryPath);
  const selected = modules.length ? discovered.filter((module) => modules.includes(module)) : discovered;
  const unknown = modules.filter((module) => !discovered.includes(module));
  if (unknown.length) throw new Error(`Requested Go modules are not in the devkit registry: ${unknown.join(', ')}`);
  if (selected.length === 0) throw new Error('Go module registry selection returned an empty set');

  const tempRoot = mkdtempSync(path.join(os.tmpdir(), 'kb-go-module-suite-'));
  const moduleCache = process.env.GOMODCACHE ?? path.join(os.homedir(), 'go', 'pkg', 'mod');
  const buildCache = process.env.GOCACHE ?? path.join(
    os.homedir(), ...(os.platform() === 'darwin' ? ['Library', 'Caches', 'go-build'] : ['.cache', 'go-build']),
  );
  let failed = false;
  try {
    for (const [variant, directory] of ['one', 'two'].map((name) => [name, path.join(tempRoot, name)])) {
      mkdirSync(directory, { recursive: true });
      for (const module of selected) {
        const moduleDir = path.join(root, module);
        const home = mkdtempSync(path.join(tempRoot, `home-${variant}-`));
        const temp = mkdtempSync(path.join(tempRoot, `tmp-${variant}-`));
        const env = { ...process.env, HOME: home, TMPDIR: temp, GOMODCACHE: moduleCache, GOCACHE: buildCache };
        for (const name of ['KB_PLATFORM_ROOT', 'KB_HOME', 'KB_NET_OFFSET', 'GOFLAGS']) delete env[name];
        const label = `${module} [TMPDIR ${variant}]`;
        try {
          const packages = goPackages(go, moduleDir, env);
          const test = run(go, ['test', ...packages], { cwd: moduleDir, env });
          if (test.status !== 0) {
            failed = true;
            process.stderr.write(`${label}: FAIL\n${test.stdout}${test.stderr}`);
          } else {
            process.stdout.write(`${label}: PASS (${packages.length} packages)\n`);
          }
        } catch (error) {
          failed = true;
          process.stderr.write(`${label}: FAIL — ${error.message}\n`);
        }
      }
    }
  } finally {
    rmSync(tempRoot, { recursive: true, force: true });
  }
  return failed ? 1 : 0;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const options = parseArgs(process.argv.slice(2));
    if (options.help) process.stdout.write(`${usage()}\n`);
    else process.exitCode = runSuite(options);
  } catch (error) {
    process.stderr.write(`${error.message}\n`);
    process.exitCode = 1;
  }
}

export { discoverModules, runSuite };
