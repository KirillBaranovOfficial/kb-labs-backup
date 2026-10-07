#!/usr/bin/env node
import { access, readFile, readdir, realpath, stat } from 'node:fs/promises';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import yaml from 'js-yaml';

const input = JSON.parse(await readStdin());
const workspaceRoot = await realpath(input.workspaceRoot);
const currentPackage = input.package;
const checkName = input.check;
const devkit = yaml.load(await readFile(path.join(workspaceRoot, 'devkit.yaml'), 'utf8'));
const adapterCategory = devkit.workspace.categories.adapter;
if (!adapterCategory) throw new Error('devkit.yaml has no adapter category');
const completenessAnchor = adapterCategory.match?.find(pattern => !/[?*]/.test(pattern));
const ignoredDirectories = new Set(['.git', 'node_modules', 'dist', 'coverage', '.turbo', '__pycache__']);

function matchesPattern(pattern, relPath) {
  const normalized = pattern.replace(/\/$/, '');
  if (normalized.endsWith('/**')) {
    const prefix = normalized.slice(0, -3);
    return relPath === prefix || relPath.startsWith(`${prefix}/`);
  }
  const patternParts = normalized.split('/').filter(Boolean);
  const pathParts = relPath.split(path.sep).filter(Boolean);
  if (patternParts.length !== pathParts.length) return false;
  return patternParts.every((part, index) => part === '*' || part === pathParts[index]);
}

function categoryFor(relPath) {
  for (const [name, category] of Object.entries(devkit.workspace.categories)) {
    if (category.match?.some(pattern => matchesPattern(pattern, relPath))) return name;
  }
  return '';
}

function issue(packageName, file, message) {
  return { check: checkName, severity: 'error', message: `${packageName}: ${message}`, file };
}

async function workspacePackages() {
  const workspace = yaml.load(await readFile(path.join(workspaceRoot, 'pnpm-workspace.yaml'), 'utf8'));
  const patterns = workspace.packages ?? [];
  const packagePaths = new Set();
  const workspaceExclusions = patterns.filter(pattern => pattern.startsWith('!')).map(pattern => pattern.slice(1));
  for (const pattern of patterns) {
    if (pattern.startsWith('!')) continue;
    for (const packagePath of await expandWorkspacePattern(pattern)) packagePaths.add(packagePath);
  }
  const excluded = devkit.workspace.exclude ?? [];
  const included = devkit.workspace.include ?? [];
  const packages = [];
  for (const packagePath of packagePaths) {
    const relPath = path.relative(workspaceRoot, packagePath);
    if (workspaceExclusions.some(pattern => matchesPattern(pattern, relPath))) continue;
    if (excluded.some(pattern => matchesPattern(pattern, relPath))) continue;
    if (included.length && !included.some(pattern => matchesPattern(pattern, relPath))) continue;
    const packageJson = JSON.parse(await readFile(path.join(packagePath, 'package.json'), 'utf8'));
    if (typeof packageJson.name === 'string') packages.push({ ...packageJson, path: packagePath, relPath });
  }
  return packages;
}

async function expandWorkspacePattern(pattern) {
  const normalized = pattern.replace(/\/$/, '');
  const recursive = normalized.endsWith('/**');
  const basePattern = recursive ? normalized.slice(0, -3) : normalized;
  const segments = basePattern.split('/').filter(Boolean);
  let candidates = [workspaceRoot];
  for (const segment of segments) {
    const next = [];
    for (const candidate of candidates) {
      if (segment === '*') {
        for (const entry of await readdir(candidate, { withFileTypes: true })) {
          if (entry.isDirectory() && !ignoredDirectories.has(entry.name)) next.push(path.join(candidate, entry.name));
        }
      } else {
        const child = path.join(candidate, segment);
        try {
          if ((await stat(child)).isDirectory()) next.push(child);
        } catch (error) {
          if (error.code !== 'ENOENT') throw error;
        }
      }
    }
    candidates = next;
  }

  const result = [];
  for (const candidate of candidates) {
    if (recursive) result.push(...await packageDirectories(candidate, devkit.workspace.maxDepth || 3));
    else {
      try {
        await access(path.join(candidate, 'package.json'));
        result.push(candidate);
      } catch (error) {
        if (error.code !== 'ENOENT') throw error;
      }
    }
  }
  return result;
}

async function packageDirectories(directory, maxDepth, depth = 0) {
  try {
    await access(path.join(directory, 'package.json'));
    return [directory];
  } catch (error) {
    if (error.code !== 'ENOENT') throw error;
  }
  if (depth >= maxDepth) return [];
  const result = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    if (entry.isDirectory() && !ignoredDirectories.has(entry.name)) {
      result.push(...await packageDirectories(path.join(directory, entry.name), maxDepth, depth + 1));
    }
  }
  return result;
}

async function readPackage(pkg) {
  return JSON.parse(await readFile(path.join(pkg.path, 'package.json'), 'utf8'));
}

async function loadDeclaredManifest(pkg, packageJson) {
  const manifestPath = packageJson.kb?.manifest ?? packageJson.kbLabs?.manifest;
  if (typeof manifestPath !== 'string' || manifestPath.length === 0) return { manifestPath };
  const modulePath = path.resolve(pkg.path, manifestPath);
  if (!modulePath.startsWith(`${pkg.path}${path.sep}`)) return { manifestPath, error: 'declared manifest path escapes package root' };
  try {
    await access(modulePath);
  } catch (error) {
    if (error.code === 'ENOENT') return { manifestPath, modulePath, built: false };
    return { manifestPath, modulePath, error: error.message };
  }
  try {
    const module = await import(pathToFileURL(modulePath).href);
    return { manifestPath, modulePath, manifest: module.manifest, built: true };
  } catch (error) {
    return { manifestPath, modulePath, error: error.message };
  }
}

async function main() {
  const items = [];

  if (currentPackage.Category === 'adapter') {
    const pkg = { name: currentPackage.Name, path: await realpath(currentPackage.Dir) };
    const packageFile = path.join(pkg.path, 'package.json');
    const packageJson = await readPackage(pkg);
    const loaded = await loadDeclaredManifest(pkg, packageJson);
    if (typeof loaded.manifestPath !== 'string' || loaded.manifestPath.length === 0) {
      items.push(issue(pkg.name, packageFile, 'adapter package must declare kb.manifest'));
    } else if (loaded.error) {
      items.push(issue(pkg.name, loaded.modulePath ?? packageFile, `cannot load declared built manifest ${loaded.manifestPath}: ${loaded.error}`));
    } else if (!loaded.manifest || typeof loaded.manifest !== 'object' || Array.isArray(loaded.manifest)) {
      items.push(issue(pkg.name, loaded.modulePath, `built manifest module ${loaded.manifestPath} must export an object named manifest`));
    } else if (!loaded.manifest.configSchema || typeof loaded.manifest.configSchema !== 'object' || Array.isArray(loaded.manifest.configSchema)) {
      items.push(issue(pkg.name, loaded.modulePath, `manifest ${loaded.manifestPath} must declare configSchema as an object (use {} when there are no options)`));
    }
  }

  // The full workspace graph comes from pnpm-workspace.yaml; category membership comes from
  // the first-match devkit category rules, the same source used by kb-devkit.
  // A literal category match is the one invocation that performs this global comparison.
  if (!completenessAnchor) {
    items.push(issue(currentPackage.Name, path.join(workspaceRoot, 'devkit.yaml'), 'adapter category needs a literal package match to anchor its workspace completeness scan'));
  } else if (currentPackage.RelPath === completenessAnchor) {
    const packages = await workspacePackages();
    const adapterPackages = packages.filter(pkg => categoryFor(pkg.relPath) === 'adapter');
    for (const pkg of packages) {
      const packageFile = path.join(pkg.path, 'package.json');
      if (!pkg.name) {
        if (categoryFor(pkg.relPath) === 'adapter') {
          items.push(issue(pkg.relPath, packageFile, 'adapter category package must declare a package name'));
        }
        continue;
      }
      if (categoryFor(pkg.relPath) === 'adapter') continue;
      let packageJson;
      try {
        packageJson = await readPackage(pkg);
      } catch (error) {
        items.push(issue(pkg.name ?? pkg.path, packageFile, `cannot read workspace package metadata: ${error.message}`));
        continue;
      }
      const manifestPath = packageJson.kb?.manifest ?? packageJson.kbLabs?.manifest;
      if (typeof manifestPath !== 'string' || manifestPath.length === 0) continue;
      const loaded = await loadDeclaredManifest(pkg, packageJson);
      // Platform workspace discovery does not create candidates until the declared
      // built manifest file exists; only built manifests can be classified by implements.
      if (loaded.built === false) continue;
      if (loaded.error) {
        items.push(issue(pkg.name, loaded.modulePath ?? packageFile, `cannot determine whether this package is a platform adapter: ${loaded.error}`));
      } else if (loaded.manifest?.implements) {
        items.push(issue(pkg.name, loaded.modulePath, 'platform adapter manifest is outside the devkit adapter category'));
      }
    }

    for (const pkg of adapterPackages) {
      const packageName = pkg.name ?? pkg.relPath;
      let packageJson;
      try {
        packageJson = await readPackage(pkg);
      } catch (error) {
        items.push(issue(packageName, path.join(pkg.path, 'package.json'), `cannot read adapter package metadata: ${error.message}`));
        continue;
      }
      const loaded = await loadDeclaredManifest(pkg, packageJson);
      if (!loaded.manifest?.implements) {
        items.push(issue(packageName, loaded.modulePath ?? path.join(pkg.path, 'package.json'), 'adapter category package is not discoverable by platform adapter discovery (manifest must export implements)'));
      }
    }
  }

  process.stdout.write(JSON.stringify({ issues: items }));
}

function readStdin() {
  return new Promise((resolve, reject) => {
    const chunks = [];
    process.stdin.setEncoding('utf8');
    process.stdin.on('data', chunk => chunks.push(chunk));
    process.stdin.on('end', () => resolve(chunks.join('')));
    process.stdin.on('error', reject);
  });
}

await main();
