/** Resolve manifest-declared built-in adapter defaults for installed platforms. */

import { discoverAdapters, type DiscoveredAdapter } from './discover-adapters.js';
import { resolveRuntimeStatePath } from '@kb-labs/core-project-registry';

export interface ResolvedAdapterConfig {
  adapters: Record<string, unknown>;
  adapterOptions: Record<string, unknown>;
}

/**
 * Fill blank adapter slots from installed manifests declaring `defaultFor` entry points.
 * Explicit platform bindings always win. Only manifest metadata and
 * marketplace locks are used here, so core-platform stays independent of
 * concrete adapter packages.
 */
export async function resolveInstalledAdapterDefaults(
  config: ResolvedAdapterConfig,
  platformRoot: string,
  projectRoot = platformRoot,
): Promise<ResolvedAdapterConfig> {
  const discovered = await discoverAdapters(platformRoot);
  return resolveDiscoveredAdapterDefaults(config, discovered, projectRoot);
}

export function resolveDiscoveredAdapterDefaults(
  config: ResolvedAdapterConfig,
  discovered: ReadonlyMap<string, DiscoveredAdapter>,
  projectRoot: string,
): ResolvedAdapterConfig {
  const adapters = { ...config.adapters };
  const adapterOptions = { ...config.adapterOptions };
  const packages = new Map<string, DiscoveredAdapter>();
  for (const adapter of discovered.values()) {
    packages.set(adapter.packageName, adapter);
  }

  const defaults = new Map<string, DiscoveredAdapter[]>();
  for (const adapter of packages.values()) {
    const manifest = adapter.module.manifest;
    if (!manifest || typeof manifest !== 'object') {continue;}
    const hasUnprovidedStructure = Object.values(manifest.configSchema ?? {}).some(option =>
      option.required && option.default === undefined && !(option.secret && option.env),
    );
    if (hasUnprovidedStructure) {continue;}
    const defaultFor = manifest.defaultFor ?? {};
    for (const slot of Object.keys(defaultFor)) {
      if (adapters[slot]) {continue;}
      const candidates = defaults.get(slot) ?? [];
      candidates.push(adapter);
      defaults.set(slot, candidates);
    }
  }

  for (const [slot, candidates] of defaults) {
    if (adapters[slot]) {continue;}
    if (candidates.length !== 1) {
      throw new Error(`Multiple installed adapters declare ${slot} as a default: ${candidates.map(candidate => candidate.packageName).join(', ')}. Bind one with \`kb adapter bind ${slot} <package>\`.`);
    }
    const adapter = candidates[0];
    if (!adapter) {continue;}
    const manifest = adapter.module.manifest;
    const entrypoint = manifest.defaultFor?.[slot];
    if (!entrypoint) {
      throw new Error(`Adapter ${adapter.packageName} declares default slot ${slot} without an entry point. Add the module path to its adapter manifest and rebuild it with \`corepack pnpm --filter ${adapter.packageName} build\`.`);
    }
    adapters[slot] = [adapter.packageName, ...entrypoint].join('/');
    adapterOptions[slot] = {
      ...declaredDefaults(adapter.module.manifest, projectRoot),
      ...asRecord(adapterOptions[slot]),
    };
  }

  return { adapters, adapterOptions };

}

function declaredDefaults(
  manifest: DiscoveredAdapter['module']['manifest'],
  projectRoot: string,
): Record<string, unknown> {
  if (!manifest || typeof manifest !== 'object') {return {};}
  const result: Record<string, unknown> = {};
  for (const [key, option] of Object.entries(manifest.configSchema ?? {})) {
    if (option.defaultPath) {
      result[key] = resolveRuntimeStatePath(projectRoot, option.defaultPath.segments);
    } else if (option.default !== undefined) {
      result[key] = option.default;
    }
  }
  return result;
}

function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, unknown>
    : {};
}
