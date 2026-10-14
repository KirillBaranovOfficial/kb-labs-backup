import { existsSync, readFileSync, readdirSync, realpathSync } from 'node:fs';
import { dirname, join, relative, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';

const platformRoot = realpathSync(resolve(process.argv[2]));
const nodeModulesRoot = join(platformRoot, 'node_modules');
const readPackage = packageRoot => JSON.parse(readFileSync(join(packageRoot, 'package.json'), 'utf8'));
const targetFor = value => {
  if (typeof value === 'string') return value;
  if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined;
  return targetFor(value.import ?? value.node ?? value.default);
};
const javascriptFiles = directory => {
  if (!existsSync(directory)) return [];
  return readdirSync(directory, { withFileTypes: true }).flatMap(entry => {
    const path = join(directory, entry.name);
    return entry.isDirectory()
      ? javascriptFiles(path)
      : /\.(?:c|m)?js$/.test(entry.name) ? [path] : [];
  });
};

const packageRoots = [];
const sdkRoot = join(nodeModulesRoot, '@kb-labs', 'sdk');
if (!existsSync(join(sdkRoot, 'package.json'))) throw new Error('installed platform is missing the SDK package manifest');
const sdkPackageJSON = readPackage(sdkRoot);
const optionalSdkPeers = Object.entries(sdkPackageJSON.peerDependenciesMeta ?? {})
  .filter(([, metadata]) => metadata?.optional === true)
  .map(([name]) => name);
const installedOptionalSdkPeers = optionalSdkPeers.filter(name =>
  existsSync(join(nodeModulesRoot, ...name.split('/'))));
if (installedOptionalSdkPeers.length) {
  throw new Error(`installed platform contains optional SDK peers: ${installedOptionalSdkPeers.sort().join(', ')}`);
}
const receiptPath = join(platformRoot, '.kb', 'v2', 'receipt.json');
const receipt = JSON.parse(readFileSync(receiptPath, 'utf8'));
if (!Array.isArray(receipt.plan?.artifacts)) {
  throw new Error('installed platform receipt is missing plan.artifacts, so bundled plugin entrypoints cannot be determined');
}
const bundledPluginPackages = receipt.plan.artifacts
  .filter(artifact => artifact.kind === 'plugin')
  .map(artifact => artifact.package);
if (bundledPluginPackages.some(packageName => typeof packageName !== 'string')
    || new Set(bundledPluginPackages).size !== bundledPluginPackages.length) {
  throw new Error('installed platform receipt has invalid or duplicate bundled plugin package names');
}
packageRoots.push(sdkRoot);
for (const packageName of bundledPluginPackages) {
  const packageRoot = join(nodeModulesRoot, ...packageName.split('/'));
  if (!existsSync(join(packageRoot, 'package.json'))) {
    throw new Error(`installed platform receipt lists bundled plugin ${packageName}, but its package manifest is missing`);
  }
  const packageJSON = readPackage(packageRoot);
  if (packageJSON.name !== packageName) {
    throw new Error(`installed platform receipt lists bundled plugin ${packageName}, but its package manifest names ${packageJSON.name}`);
  }
  const manifestPath = join(packageRoot, 'dist', 'manifest.json');
  if (!existsSync(manifestPath)) {
    throw new Error(`installed platform bundled plugin ${packageName} is missing dist/manifest.json`);
  }
  const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'));
  if (manifest.schema !== 'kb.plugin/3') {
    throw new Error(`installed platform bundled plugin ${packageName} has manifest schema ${manifest.schema}`);
  }
  packageRoots.push(packageRoot);
}

const entrypoints = [];
for (const packageRoot of packageRoots) {
  const packageJSON = readPackage(packageRoot);
  const exportsMap = packageJSON.exports;
  if (!exportsMap || typeof exportsMap !== 'object' || Array.isArray(exportsMap)) {
    throw new Error(`${packageJSON.name} has no runtime exports map`);
  }
  const nonRuntime = packageJSON.kbLabs?.release?.nonRuntimeEntrypoints ?? [];
  for (const [exportName, value] of Object.entries(exportsMap)) {
    if (nonRuntime.includes(exportName)) continue;
    const target = targetFor(value);
    if (!target) throw new Error(`${packageJSON.name} runtime export ${exportName} has no import target`);
    if (!target.includes('*')) {
      if (!target.startsWith('./')) throw new Error(`${packageJSON.name} export ${exportName} has non-package target ${target}`);
      const file = resolve(packageRoot, target);
      if (!existsSync(file)) throw new Error(`${packageJSON.name} runtime export ${exportName} points to missing file ${target}`);
      entrypoints.push({ packageName: packageJSON.name, exportName, file });
      continue;
    }
    const targetDirectory = resolve(packageRoot, target.slice(0, target.indexOf('*')));
    for (const file of javascriptFiles(targetDirectory)) {
      const suffix = target.slice(target.indexOf('*') + 1);
      if (suffix && !file.endsWith(suffix)) continue;
      entrypoints.push({ packageName: packageJSON.name, exportName, file });
    }
  }
}

const failures = [];
for (const entrypoint of entrypoints) {
  const source = 'await import(new URL(process.argv[1], "file://"));';
  const result = spawnSync(process.execPath, ['--input-type=module', '-e', source, entrypoint.file], {
    encoding: 'utf8',
    cwd: platformRoot,
  });
  if (result.status !== 0) {
    const detail = (result.stderr || result.stdout).trim() || `Node exited with status ${result.status}`;
    failures.push(`${entrypoint.packageName} ${entrypoint.exportName} (${relative(platformRoot, entrypoint.file)}): ${detail}`);
  }
}

if (failures.length) {
  process.stderr.write(`Installed runtime entrypoint imports failed:\n${failures.map(failure => `- ${failure}`).join('\n')}\n`);
  process.exitCode = 1;
} else {
  process.stdout.write(`Imported ${entrypoints.length} runtime entrypoints from ${packageRoots.length} installed packages.\n`);
}
