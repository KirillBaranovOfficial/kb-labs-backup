/** Show effective adapters for the current project and their built-in fallback. */

import path from 'node:path';
import { ADAPTER_DEFAULTS, type AdapterSlot } from '@kb-labs/core-platform';
import { loadPlatformConfig } from '@kb-labs/core-runtime';
import { defineSystemCommand, type CommandResult } from '@kb-labs/shared-command-kit';
import { getContextCwd } from '@kb-labs/shared-cli-ui';
import { generateExamples } from '../../../utils/generate-examples';

type AdapterRow = {
  slot: string;
  implementation: string | null;
  mode: 'real' | 'inmemory' | 'noop' | 'unconfigured';
  baseDir?: string;
  networkRequired: boolean;
  requiresConfiguration?: boolean;
};

type ListResult = CommandResult & { adapters?: AdapterRow[]; projectRoot?: string };

const LOCAL_PACKAGES = new Set([
  '@kb-labs/adapters-fs', '@kb-labs/adapters-sqlite', '@kb-labs/adapters-pino',
  '@kb-labs/adapters-log-ringbuffer', '@kb-labs/adapters-log-document',
  '@kb-labs/adapters-analytics-sqlite', '@kb-labs/adapters-service-transport-http',
  '@kb-labs/adapters-state-broker',
]);

function packageName(spec: string): string {
  const separator = spec.lastIndexOf('@');
  return separator > spec.indexOf('/') ? spec.slice(0, separator) : spec;
}

function packageRoot(spec: string): string {
  const name = packageName(spec);
  const segments = name.split('/');
  return name.startsWith('@') ? segments.slice(0, 2).join('/') : segments[0] ?? name;
}

export const adapterList = defineSystemCommand<{ json: { type: 'boolean'; description?: string } }, ListResult>({
  name: 'list',
  description: 'List effective adapters for the current project',
  longDescription: 'Shows project overrides, installed platform defaults, and built-in fallbacks without contacting adapter services.',
  category: 'adapter',
  examples: generateExamples('adapter list', 'kb', [{ flags: { json: true }, description: '' }]),
  flags: { json: { type: 'boolean', description: 'Output machine-readable JSON' } },
  analytics: { command: 'adapter.list' },
  async handler(ctx) {
    const loaded = await loadPlatformConfig({ startDir: getContextCwd(ctx), loadEnvFile: false });
    const config = loaded.platformConfig;
    const rows = (Object.keys(ADAPTER_DEFAULTS) as AdapterSlot[]).map((slot): AdapterRow => {
      const configured = config.adapters?.[slot];
      const configuredPackage = typeof configured === 'string' ? configured : Array.isArray(configured) ? configured[0] : undefined;
      const packageId = configuredPackage ? packageName(configuredPackage) : undefined;
      const option = config.adapterOptions?.[slot];
      const baseDir = slot === 'storage' && option && typeof option === 'object' &&
        'baseDir' in option && typeof option.baseDir === 'string'
        ? path.resolve(loaded.projectRoot, option.baseDir)
        : undefined;
      const localLogsAvailable = slot === 'logs' &&
        Boolean(config.adapters?.logRingBuffer || config.adapters?.logPersistence);
      return {
        slot,
        implementation: localLogsAvailable ? 'HybridLogReader' : packageId ?? (ADAPTER_DEFAULTS[slot].implementation === 'unavailable' ? null : ADAPTER_DEFAULTS[slot].implementation),
        mode: localLogsAvailable ? 'real' : packageId ? 'real' : ADAPTER_DEFAULTS[slot].defaultFallback === 'noop' ? 'unconfigured' : 'inmemory',
        ...(baseDir ? { baseDir } : {}),
        networkRequired: localLogsAvailable ? false : packageId ? !LOCAL_PACKAGES.has(packageRoot(packageId)) : false,
        ...(ADAPTER_DEFAULTS[slot].defaultFallback === 'noop' && !packageId ? { requiresConfiguration: true } : {}),
      };
    });
    const knownSlots = new Set(rows.map((row) => row.slot));
    for (const [slot, configured] of Object.entries(config.adapters ?? {})) {
      if (knownSlots.has(slot)) continue;
      const configuredPackage = typeof configured === 'string' ? configured : Array.isArray(configured) ? configured[0] : undefined;
      if (!configuredPackage) continue;
      const implementation = packageName(configuredPackage);
      rows.push({ slot, implementation, mode: 'real', networkRequired: !LOCAL_PACKAGES.has(packageRoot(implementation)) });
    }
    return { ok: true, adapters: rows, projectRoot: loaded.projectRoot };
  },
  formatter(result, ctx, flags) {
    if (flags.json) { ctx.ui?.json?.(result); return; }
    if (!result.ok) { ctx.ui?.error(result.error ?? 'Could not list adapters'); return; }
    ctx.ui?.table?.(result.adapters ?? []);
  },
});
