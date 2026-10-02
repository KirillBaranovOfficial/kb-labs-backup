/** Resolve manifest-declared built-in adapter defaults for installed platforms. */

import { discoverAdapters, type DiscoveredAdapter } from './discover-adapters.js';
import { resolveRuntimeStatePath } from '@kb-labs/core-project-registry';

export interface ResolvedAdapterConfig {
  adapters: Record<string, unknown>;
  adapterOptions: Record<string, unknown>;
}

/**
 * Fill blank adapter slots from installed manifests declaring `defaultFor`.
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
    const declared = manifest as { defaultFor?: unknown; configSchema?: Record<string, { required?: boolean; default?: unknown; secret?: boolean; env?: string }> };
    const hasUnprovidedStructure = Object.values(declared.configSchema ?? {}).some(option =>
      option.required && option.default === undefined && !(option.secret && option.env),
    );
    if (hasUnprovidedStructure) {continue;}
    const defaultFor = declared.defaultFor;
    if (!Array.isArray(defaultFor)) {continue;}
    for (const slot of defaultFor) {
      if (typeof slot !== 'string' || adapters[slot]) {continue;}
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
    const manifest = adapter.module.manifest as { implements?: string | string[] };
    const capabilities = Array.isArray(manifest.implements) ? manifest.implements : [manifest.implements];
    const subpath = slot === 'kvStore' && capabilities.includes('IKVStore') ? '/kv' : '';
    adapters[slot] = `${adapter.packageName}${subpath}`;
    adapterOptions[slot] = {
      ...defaultOptions(slot),
      ...asRecord(adapterOptions[slot]),
    };
  }

  return { adapters, adapterOptions };

  function defaultOptions(slot: string): Record<string, unknown> {
    switch (slot) {
      case 'storage': return { baseDir: resolveRuntimeStatePath(projectRoot, ['storage']) };
      case 'documentDatabase':
      case 'kvStore': return { filename: resolveRuntimeStatePath(projectRoot, ['database', 'kb.sqlite']) };
      case 'analytics': return { filename: resolveRuntimeStatePath(projectRoot, ['analytics', 'analytics.sqlite']) };
      case 'logRingBuffer': return { maxSize: 100 };
      case 'logPersistence': return { collection: 'logs' };
      default: return {};
    }
  }
}

function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, unknown>
    : {};
}
