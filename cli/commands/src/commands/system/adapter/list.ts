/** Show platform adapters and their built-in fallback. */

import path from 'node:path';
import { ADAPTER_DEFAULTS, toErrorEnvelope, ErrorEnvelopeError, type AdapterSlot, type ErrorEnvelope } from '@kb-labs/core-platform';
import { discoverAdapters, loadPlatformConfig, resolveInstalledAdapterDefaults, type AdaptersConfig } from '@kb-labs/core-runtime';
import { defineSystemCommand, throwSystemCataloguedError, type CommandResult } from '@kb-labs/shared-command-kit';
import { getContextCwd } from '@kb-labs/shared-cli-ui';
import { generateExamples } from '../../../utils/generate-examples';
import { resolveActiveGateway } from '@kb-labs/cli-runtime/gateway';

type AdapterRow = {
  slot: string;
  implementation: string | null;
  mode: 'real' | 'inmemory' | 'noop' | 'unconfigured' | 'degraded' | 'unknown';
  baseDir?: string;
  networkRequired: boolean;
  requiresConfiguration?: boolean;
  credentialsStatus?: 'configured' | 'missing';
  missingCredentials?: string[];
  reason?: 'load-failed';
  correlationId?: string;
  action?: { id: string; label: string; command: string };
  runtimeStatus?: 'known' | 'unknown';
  runtimeReason?: string;
};

type HostAdapterStatus = {
  slot: string;
  mode: 'real' | 'inmemory' | 'noop' | 'degraded';
  implementation: string;
  configuredPackage?: string;
  reason?: 'not-configured' | 'load-failed';
  correlationId?: string;
  action?: { id: string; label: string; command: string };
};

type ListResult = CommandResult & { adapters?: AdapterRow[]; projectRoot?: string; envelope?: ErrorEnvelope };

const LOCAL_PACKAGES = new Set([
  '@kb-labs/adapters-fs', '@kb-labs/adapters-sqlite', '@kb-labs/adapters-pino',
  '@kb-labs/adapters-log-ringbuffer', '@kb-labs/adapters-log-document',
  '@kb-labs/adapters-analytics-sqlite', '@kb-labs/adapters-service-transport-http',
  '@kb-labs/adapters-state-broker',
]);

const CONFIG_KEY_BY_SLOT: Partial<Record<AdapterSlot, keyof AdaptersConfig>> = {
  storage: 'storage', cache: 'cache', logger: 'logger', analytics: 'analytics',
  vectorStore: 'vectorStore', llm: 'llm', embeddings: 'embeddings',
  eventBus: 'eventBus', notifier: 'notifier', documentDatabase: 'documentDatabase', kvStore: 'kvStore',
};

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
  scope: 'platform',
  jsonTest: { safeArgs: [] },
  description: 'List effective platform adapters',
  longDescription: 'Shows platform bindings, installed defaults, and built-in fallbacks without contacting adapter services.',
  category: 'adapter',
  examples: generateExamples('adapter list', 'kb', [{ flags: { json: true }, description: '' }]),
  flags: { json: { type: 'boolean', description: 'Output machine-readable JSON' } },
  analytics: { command: 'adapter.list' },
  async handler(ctx) {
    let loaded: Awaited<ReturnType<typeof loadPlatformConfig>>;
    try {
      loaded = await loadPlatformConfig({ startDir: getContextCwd(ctx), loadEnvFile: false });
    } catch (error) {
      const envelope = toErrorEnvelope(error);
      throwSystemCataloguedError(envelope);
    }
    const config = loaded.platformConfig;
    const resolvedDefaults = await resolveInstalledAdapterDefaults({
      adapters: Object.fromEntries(Object.entries(config.adapters ?? {})),
      adapterOptions: Object.fromEntries(Object.entries(config.adapterOptions ?? {})),
    }, loaded.platformRoot, loaded.projectRoot);
    const effectiveAdapters = resolvedDefaults.adapters as AdaptersConfig;
    const effectiveAdapterOptions = resolvedDefaults.adapterOptions as Record<string, unknown>;
    const discovered = await discoverAdapters(loaded.platformRoot);
    const startHostCommand = `kb-create start --platform-root ${JSON.stringify(loaded.platformRoot)}`;
    let hostStatuses = new Map<string, HostAdapterStatus>();
    let runtimeStatus: 'known' | 'unknown' = 'unknown';
    let runtimeReason = 'Host runtime status could not be read.';
    try {
      const gateway = await resolveActiveGateway({ cwd: getContextCwd(ctx), env: process.env, platformRoot: loaded.platformRoot });
      const response = await ctx.runtime.fetch(`${gateway.gatewayUrl}/health/adapters`);
      if (!response.ok) {
        runtimeReason = `Host returned HTTP ${response.status} for adapter health.`;
      } else {
        hostStatuses = new Map((await response.json() as HostAdapterStatus[]).map((status) => [status.slot, status]));
        runtimeStatus = 'known';
        runtimeReason = '';
      }
    } catch (error) {
      if (error instanceof ErrorEnvelopeError && error.envelope.code !== 'KB_HOST_UNREACHABLE') {
        throwSystemCataloguedError(error.envelope);
      }
      runtimeReason = error instanceof Error ? error.message : String(error);
    }
    const rows = (Object.keys(ADAPTER_DEFAULTS) as AdapterSlot[]).map((slot): AdapterRow => {
      const configKey = CONFIG_KEY_BY_SLOT[slot];
      const configured = configKey ? effectiveAdapters[configKey] : undefined;
      const configuredPackage = typeof configured === 'string' ? configured : Array.isArray(configured) ? configured[0] : undefined;
      const packageId = configuredPackage ? packageName(configuredPackage) : undefined;
      const option = configKey ? effectiveAdapterOptions[configKey] : undefined;
      const schema = packageId
        ? discovered.get(packageRoot(packageId))?.module.manifest
        : undefined;
      const configSchema = schema && typeof schema === 'object' && 'configSchema' in schema &&
        schema.configSchema && typeof schema.configSchema === 'object'
        ? schema.configSchema as Record<string, { required?: boolean; secret?: boolean; env?: string }>
        : {};
      const optionValues = option && typeof option === 'object' && !Array.isArray(option)
        ? option as Record<string, unknown>
        : {};
      const credentialOptions = Object.entries(configSchema).filter(([, field]) => field.required && field.secret && field.env);
      const missingCredentials = credentialOptions
        .filter(([key, field]) => {
          const value = optionValues[key];
          return !(typeof value === 'string' && value.length > 0) && !(ctx.runtime.env(field.env ?? '') || '').length;
        })
        .map(([, field]) => field.env as string);
      const baseDir = slot === 'storage' && option && typeof option === 'object' &&
        'baseDir' in option && typeof option.baseDir === 'string'
        ? path.resolve(loaded.projectRoot, option.baseDir)
        : undefined;
      const localLogsAvailable = slot === 'logs' &&
        Boolean(effectiveAdapters.logRingBuffer || effectiveAdapters.logPersistence);
      const hostStatus = hostStatuses.get(slot);
      const slotRuntimeKnown = runtimeStatus === 'known' && hostStatus !== undefined;
      const implementation = slotRuntimeKnown
        ? hostStatus.implementation === 'unavailable' ? null : hostStatus.implementation
        : localLogsAvailable ? 'HybridLogReader' : packageId ?? (ADAPTER_DEFAULTS[slot].implementation === 'unavailable' ? null : ADAPTER_DEFAULTS[slot].implementation);
      return {
        slot,
        implementation,
        mode: slotRuntimeKnown
          ? hostStatus.mode === 'noop' && !hostStatus.configuredPackage ? 'unconfigured' : hostStatus.mode
          : 'unknown',
        runtimeStatus: slotRuntimeKnown ? 'known' : 'unknown',
        ...(!slotRuntimeKnown ? { runtimeReason: runtimeStatus === 'known' ? `Host did not report slot ${slot}.` : runtimeReason } : {}),
        ...(hostStatus?.reason === 'load-failed' ? { reason: hostStatus.reason } : {}),
        ...(hostStatus?.correlationId ? { correlationId: hostStatus.correlationId } : {}),
        ...(hostStatus?.action ? { action: hostStatus.action } : {}),
        ...(!slotRuntimeKnown ? { action: { id: 'start-host', label: 'Start host', command: startHostCommand } } : {}),
        ...(baseDir ? { baseDir } : {}),
        networkRequired: localLogsAvailable ? false : packageId ? !LOCAL_PACKAGES.has(packageRoot(packageId)) : false,
        ...(ADAPTER_DEFAULTS[slot].defaultFallback === 'noop' && !packageId ? { requiresConfiguration: true } : {}),
        ...(credentialOptions.length > 0 ? {
          credentialsStatus: missingCredentials.length > 0 ? 'missing' : 'configured',
          ...(missingCredentials.length > 0 ? { missingCredentials } : {}),
        } : {}),
      };
    });
    const knownSlots = new Set(rows.map((row) => row.slot));
    for (const [slot, configured] of Object.entries(effectiveAdapters)) {
      if (knownSlots.has(slot)) {continue;}
      const configuredPackage = typeof configured === 'string' ? configured : Array.isArray(configured) ? configured[0] : undefined;
      if (!configuredPackage) {continue;}
      const implementation = packageName(configuredPackage);
      rows.push({ slot, implementation, mode: 'unknown', runtimeStatus: 'unknown', runtimeReason,
        action: { id: 'start-host', label: 'Start host', command: startHostCommand },
        networkRequired: !LOCAL_PACKAGES.has(packageRoot(implementation)) });
    }
    return { ok: true, adapters: rows, projectRoot: loaded.projectRoot };
  },
  formatter(result, ctx, flags) {
    if (!result.ok) {
      const error = result.error;

      return;
    }
    ctx.ui?.table?.(result.adapters ?? []);
  },
});
