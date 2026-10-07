/**
 * Validation of platform adapter bindings and `platform.adapterOptions.<adapter>`
 * against the adapter's installed manifest.
 *
 * Shared by `kb config set` (`cli/commands`) and the host's `/api/v1/adapters`
 * API (`services/host`, stage 6 second slice) so both write through the same
 * validation rules — neither owns a private copy of this logic.
 *
 * The manifest `configSchema` is a descriptive map (`type`, `enum`, ...) that
 * is not guaranteed to list every option an adapter accepts, so the check is
 * deliberately narrow: only options the manifest DECLARES are type/enum
 * checked, unknown options pass, and `required` is not enforced (a project
 * layer may set only some of the options). Adapter binding writes also require
 * an installed package manifest that declares the requested slot.
 */

import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

import { isEnvReference } from './user-config/secrets.js';
import type { ConfigIssue } from './user-config/write-user-config.js';

export interface ManifestFieldSchema {
  type?: string;
  enum?: unknown[];
}

export type ManifestConfigSchema = Record<string, ManifestFieldSchema>;

export interface AdapterConfigManifest {
  implements?: string | string[];
  defaultFor?: string[];
  configSchema?: ManifestConfigSchema;
}

interface PackageJson {
  main?: string;
  exports?: unknown;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

/** `@scope/pkg/sub/path` -> `{ name: '@scope/pkg', subpath: './sub/path' }`. */
export function splitPackageSpecifier(spec: string): { name: string; subpath: string } {
  const parts = spec.split('/');
  const nameLength = spec.startsWith('@') ? 2 : 1;
  const name = parts.slice(0, nameLength).join('/');
  const rest = parts.slice(nameLength).join('/');
  return { name, subpath: rest ? `./${rest}` : '.' };
}

function adapterPackageInstalled(spec: string, platformRoot: string): boolean {
  const { name } = splitPackageSpecifier(spec);
  return existsSync(path.join(platformRoot, 'node_modules', name, 'package.json'));
}

function exportTarget(entry: unknown): string | undefined {
  if (typeof entry === 'string') {
    return entry;
  }
  if (isRecord(entry)) {
    for (const condition of ['import', 'default', 'node', 'require']) {
      const target = exportTarget(entry[condition]);
      if (target) {
        return target;
      }
    }
  }
  return undefined;
}

/** Absolute path of the module for a package installed under the platform root. */
export function resolveAdapterModule(spec: string, platformRoot: string): string | undefined {
  const { name, subpath } = splitPackageSpecifier(spec);
  const pkgDir = path.join(platformRoot, 'node_modules', name);
  const pkgJsonPath = path.join(pkgDir, 'package.json');
  if (!existsSync(pkgJsonPath)) {
    return undefined;
  }
  let pkg: PackageJson;
  try {
    pkg = JSON.parse(readFileSync(pkgJsonPath, 'utf8')) as PackageJson;
  } catch {
    return undefined;
  }
  const exportsField = pkg.exports;
  let target: string | undefined;
  if (isRecord(exportsField) && Object.keys(exportsField).some((key) => key.startsWith('.'))) {
    target = exportTarget(exportsField[subpath]);
  } else if (subpath === '.') {
    target = exportTarget(exportsField) ?? pkg.main ?? 'index.js';
  }
  if (target) {
    const full = path.resolve(pkgDir, target);
    if (existsSync(full)) {
      return full;
    }
  }
  return undefined;
}

/** The manifest of an adapter package installed beneath the target platform root. */
export async function loadAdapterManifest(
  spec: string,
  platformRoot: string,
): Promise<AdapterConfigManifest | undefined> {
  const modulePath = resolveAdapterModule(spec, platformRoot);
  if (!modulePath) {
    return undefined;
  }
  try {
    const mod = (await import(pathToFileURL(modulePath).href)) as Record<string, unknown>;
    const fromDefault = isRecord(mod.default) ? mod.default.manifest : undefined;
    const manifest = mod.manifest ?? fromDefault;
    if (isRecord(manifest)) {
      return {
        ...(typeof manifest.implements === 'string' || Array.isArray(manifest.implements)
          ? { implements: manifest.implements as string | string[] }
          : {}),
        ...(Array.isArray(manifest.defaultFor)
          ? { defaultFor: manifest.defaultFor.filter((slot): slot is string => typeof slot === 'string') }
          : {}),
        ...(isRecord(manifest.configSchema)
          ? { configSchema: manifest.configSchema as ManifestConfigSchema }
          : {}),
      };
    }
  } catch {
    // The adapter cannot be imported here; fall back to the platform schema.
  }
  return undefined;
}

function matchesType(type: string, value: unknown): boolean {
  switch (type) {
    case 'string':
      return typeof value === 'string';
    case 'number':
      return typeof value === 'number' && Number.isFinite(value);
    case 'boolean':
      return typeof value === 'boolean';
    case 'array':
      return Array.isArray(value);
    case 'object':
      return isRecord(value);
    default:
      return true;
  }
}

/** Check the declared options of `options` against a manifest `configSchema`. */
export function validateAdapterOptions(
  basePath: string,
  options: Record<string, unknown>,
  schema: ManifestConfigSchema,
): ConfigIssue[] {
  const issues: ConfigIssue[] = [];
  for (const [key, field] of Object.entries(schema)) {
    if (!Object.prototype.hasOwnProperty.call(options, key)) {
      continue;
    }
    const value = options[key];
    const fieldPath = `${basePath}.${key}`;
    // `${ENV_VAR}` references resolve at load time and may stand for any type.
    if (isEnvReference(value)) {
      continue;
    }
    if (field.type && !matchesType(field.type, value)) {
      issues.push({ path: fieldPath, message: `Expected ${field.type}` });
    } else if (field.enum && !field.enum.includes(value)) {
      issues.push({ path: fieldPath, message: `Expected one of: ${field.enum.map(String).join(', ')}` });
    }
  }
  return issues;
}

function adapterSpecFor(name: string, ...adapterMaps: unknown[]): string | undefined {
  for (const adapters of adapterMaps) {
    if (!isRecord(adapters)) {
      continue;
    }
    const value = adapters[name];
    const first = Array.isArray(value) ? value[0] : value;
    if (typeof first === 'string' && first !== '') {
      return first;
    }
  }
  return undefined;
}

function optionsHolder(keyPath: readonly string[]): { name: string; basePath: string } | undefined {
  // Paths: platform.adapterOptions.<name>[...]  or  adapterOptions.<name>[...]
  const at = keyPath[0] === 'platform' ? 1 : 0;
  if (keyPath[at] !== 'adapterOptions' || keyPath[at + 1] === undefined) {
    return undefined;
  }
  const name = keyPath[at + 1]!;
  const scope = at === 1 ? ['platform', 'adapterOptions', name] : ['adapterOptions', name];
  return { name, basePath: scope.join('.') };
}

export interface AdapterOptionsValidatorInput {
  /** Platform root containing installed adapter packages. */
  platformRoot: string;
  /** `platform.adapters` of the effective config, used when the edited file does not bind the adapter itself. */
  effectiveAdapters?: unknown;
}

/**
 * Build the `validate` hook for `setUserConfigValue`: when the edited key lives
 * under `adapterOptions.<name>`, validate that adapter's options against its
 * manifest `configSchema`.
 */
export function createAdapterOptionsValidator(
  keyPath: readonly string[],
  input: AdapterOptionsValidatorInput,
): ((document: Record<string, unknown>) => Promise<ConfigIssue[]>) | undefined {
  const target = optionsHolder(keyPath);
  const bindingSlot = keyPath[0] === 'platform' && keyPath[1] === 'adapters' && keyPath.length === 3
    ? keyPath[2]
    : undefined;
  if (!target && !bindingSlot) {
    return undefined;
  }
  const { name, basePath } = target ?? { name: bindingSlot!, basePath: keyPath.slice(0, 3).join('.') };
  const cachedManifests = new Map<string, Promise<AdapterConfigManifest | undefined>>();

  return async (document) => {
    const platform = isRecord(document.platform) ? document.platform : undefined;
    if (bindingSlot) {
      const adapters = isRecord(platform?.adapters) ? platform.adapters : undefined;
      const value = adapters?.[bindingSlot];
      if (value === null) {return [];}
      const specs = typeof value === 'string'
        ? [value]
        : Array.isArray(value) ? value.filter((entry): entry is string => typeof entry === 'string') : [];
      const issues: ConfigIssue[] = [];
      for (const spec of specs) {
        const manifest = await getManifest(spec);
        if (!manifest) {
          issues.push(adapterPackageInstalled(spec, input.platformRoot)
            ? {
                path: basePath,
                message: `Installed adapter package "${spec}" does not expose a loadable manifest for slot "${bindingSlot}".`,
                hint: `Repair the installed package manifest for ${spec}, then retry the binding.`,
              }
            : notInstalledIssue(spec, bindingSlot, basePath));
          continue;
        }
        const contract = ADAPTER_CONTRACT_BY_SLOT[bindingSlot];
        const implementations = Array.isArray(manifest.implements) ? manifest.implements : [manifest.implements];
        if (!manifest.defaultFor?.includes(bindingSlot) && (!contract || !implementations.includes(contract))) {
          issues.push({
            path: basePath,
            code: 'KB_ADAPTER_SLOT_MISMATCH',
            details: { package: splitPackageSpecifier(spec).name, slot: bindingSlot },
            message: `Adapter package ${splitPackageSpecifier(spec).name} does not declare platform slot ${bindingSlot}.`,
            hint: `Install a provider that declares ${bindingSlot} with \`kb marketplace install <pkg> --scope platform\`, then retry.`,
          });
        }
      }
      return issues;
    }

    const optionsRoot = keyPath[0] === 'platform' ? platform?.adapterOptions : document.adapterOptions;
    const options = isRecord(optionsRoot) ? optionsRoot[name] : undefined;
    if (!isRecord(options)) {
      return [];
    }
    const spec = adapterSpecFor(name, platform?.adapters, input.effectiveAdapters);
    if (!spec) {
      return [{
        path: basePath,
        message: `Cannot validate adapter options because no package is bound for ${name}. Bind an installed adapter before setting its options.`,
        hint: `Run \`kb adapter bind ${name} <package>\` with an installed adapter, then retry.`,
      }];
    }
    const manifest = await getManifest(spec);
    if (!manifest && !adapterPackageInstalled(spec, input.platformRoot)) {
      return [notInstalledIssue(spec, name, basePath)];
    }
    if (!manifest) {
      return [{
        path: basePath,
        message: `Cannot load the installed manifest for adapter ${spec}.`,
        hint: `Repair the installed package manifest for ${spec}, then retry the option write.`,
      }];
    }
    if (!manifest.configSchema) {
      return [{
        path: basePath,
        message: `Installed adapter ${spec} does not declare configSchema for ${basePath}.`,
        hint: `Run \`kb adapter bind ${name} <package>\` with a provider that declares configSchema, then retry.`,
      }];
    }
    return validateAdapterOptions(basePath, options, manifest.configSchema);
  };

  function getManifest(spec: string): Promise<AdapterConfigManifest | undefined> {
    let pending = cachedManifests.get(spec);
    if (!pending) {
      pending = loadAdapterManifest(spec, input.platformRoot);
      cachedManifests.set(spec, pending);
    }
    return pending;
  }
}

function notInstalledIssue(spec: string, slot: string, basePath: string): ConfigIssue {
  const packageName = splitPackageSpecifier(spec).name;
  return {
    path: basePath,
    code: 'KB_ADAPTER_NOT_INSTALLED',
    details: { package: packageName, slot },
    message: `Adapter package ${packageName} is not installed on this platform.`,
    hint: `kb marketplace install ${packageName} --scope platform`,
  };
}

const ADAPTER_CONTRACT_BY_SLOT: Record<string, string> = {
  logger: 'ILogger',
  cache: 'ICache',
  storage: 'IStorage',
  eventBus: 'IEventBus',
  documentDatabase: 'IDocumentDatabase',
  kvStore: 'IKVStore',
  analytics: 'IAnalytics',
  llm: 'ILLM',
  embeddings: 'IEmbeddings',
  vectorStore: 'IVectorStore',
  logs: 'ILogReader',
  snapshotManager: 'ISnapshotManager',
};
