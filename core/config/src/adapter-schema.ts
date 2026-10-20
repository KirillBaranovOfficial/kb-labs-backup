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

import { existsSync } from 'node:fs';
import path from 'node:path';
import {
  adapterManifestProvidesSlot,
  loadAdapterManifest,
  resolveInstalledAdapterPackageRoot,
  type AdapterManifestLoadResult,
} from '@kb-labs/core-platform';

import { isEnvReference } from './user-config/secrets.js';
import type { ConfigIssue } from './user-config/write-user-config.js';

export interface ManifestFieldSchema {
  type?: string;
  enum?: unknown[];
  integer?: boolean;
  minimum?: number;
  maximum?: number;
  minimumMessage?: string;
  hint?: string;
}

export type ManifestConfigSchema = Record<string, ManifestFieldSchema>;

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
  return existsSync(path.join(resolveInstalledAdapterPackageRoot(spec, platformRoot), 'package.json'));
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
    } else if (field.integer && typeof value === 'number' && !Number.isInteger(value)) {
      issues.push({ path: fieldPath, message: 'Expected an integer', hint: field.hint });
    } else if (field.minimum !== undefined && typeof value === 'number' && value < field.minimum) {
      issues.push({ path: fieldPath, message: field.minimumMessage ?? `Must be at least ${field.minimum}`, hint: field.hint });
    } else if (field.maximum !== undefined && typeof value === 'number' && value > field.maximum) {
      issues.push({ path: fieldPath, message: `Must be at most ${field.maximum}`, hint: field.hint });
    }
  }
  return issues;
}

/** Validate every bound platform adapter option set before the host starts. */
export async function validatePlatformAdapterOptions(
  platformRoot: string,
  platformConfig: Record<string, unknown>,
): Promise<ConfigIssue[]> {
  const adapters = isRecord(platformConfig.adapters) ? platformConfig.adapters : {};
  const options = isRecord(platformConfig.adapterOptions) ? platformConfig.adapterOptions : {};
  const issues: ConfigIssue[] = [];

  for (const [slot, binding] of Object.entries(adapters)) {
    const slotOptions = options[slot];
    if (!isRecord(slotOptions)) {
      continue;
    }
    const specs = typeof binding === 'string'
      ? [binding]
      : Array.isArray(binding) ? binding.filter((entry): entry is string => typeof entry === 'string') : [];
    if (specs.length === 0) {
      issues.push({
        path: `platform.adapterOptions.${slot}`,
        message: `Cannot validate adapter options because no package is bound for ${slot}.`,
        hint: `Run \`kb adapter bind ${slot} <package>\` with an installed adapter, then retry.`,
      });
      continue;
    }
    for (const spec of specs) {
      const result = await loadAdapterManifest({ spec, platformRoot });
      if (result.kind !== 'adapter' || !result.manifest.configSchema) {
        issues.push({
          path: `platform.adapterOptions.${slot}`,
          message: result.kind === 'failed'
            ? `Cannot load configured adapter ${spec}: ${result.reason}`
            : `Cannot load a configSchema for configured adapter ${spec}.`,
          hint: `Repair the installed adapter manifest for ${spec}, then run \`kb-create start\` again.`,
        });
        continue;
      }
      issues.push(...validateAdapterOptions(`platform.adapterOptions.${slot}`, slotOptions, result.manifest.configSchema));
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
  const cachedManifests = new Map<string, Promise<AdapterManifestLoadResult>>();

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
        const result = await getManifest(spec);
        if (result.kind !== 'adapter') {
          issues.push(adapterPackageInstalled(spec, input.platformRoot)
            ? {
                path: basePath,
                message: result.kind === 'failed'
                  ? `Installed adapter package "${spec}" failed to load its manifest: ${result.reason}`
                  : `Installed adapter package "${spec}" does not expose an adapter manifest for slot "${bindingSlot}".`,
                hint: `Repair the installed package manifest for ${spec}, then retry the binding.`,
              }
            : notInstalledIssue(spec, bindingSlot, basePath));
          continue;
        }
        if (!adapterManifestProvidesSlot(result.manifest, bindingSlot)) {
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
    const result = await getManifest(spec);
    if (result.kind !== 'adapter' && !adapterPackageInstalled(spec, input.platformRoot)) {
      return [notInstalledIssue(spec, name, basePath)];
    }
    if (result.kind !== 'adapter') {
      return [{
        path: basePath,
        message: result.kind === 'failed'
          ? `Cannot load the installed manifest for adapter ${spec}: ${result.reason}`
          : `Installed package ${spec} does not declare an adapter manifest.`,
        hint: `Repair the installed package manifest for ${spec}, then retry the option write.`,
      }];
    }
    if (!result.manifest.configSchema) {
      return [{
        path: basePath,
        message: `Installed adapter ${spec} does not declare configSchema for ${basePath}.`,
        hint: `Run \`kb adapter bind ${name} <package>\` with a provider that declares configSchema, then retry.`,
      }];
    }
    return validateAdapterOptions(basePath, options, result.manifest.configSchema);
  };

  function getManifest(spec: string): Promise<AdapterManifestLoadResult> {
    let pending = cachedManifests.get(spec);
    if (!pending) {
      pending = loadAdapterManifest({ spec, platformRoot: input.platformRoot });
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
