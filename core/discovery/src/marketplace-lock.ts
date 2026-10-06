/**
 * @module @kb-labs/core-discovery/marketplace-lock
 * Read / write / validate .kb/marketplace.lock
 */

import * as fs from 'node:fs/promises';
import * as path from 'node:path';
import { randomUUID } from 'node:crypto';
import { DISCOVERY_MARKETPLACE_LOCK } from './discovery-inputs.js';
import type { MarketplaceLock, MarketplaceEntry, EntityKind, EntitySignature } from './types.js';
import { DiagnosticCollector } from './diagnostics.js';

const SCHEMA_VERSION = 'kb.marketplace/3' as const;

// ---------------------------------------------------------------------------
// Read
// ---------------------------------------------------------------------------

/**
 * Load the marketplace lock file from the given workspace root.
 * Returns null if the file does not exist or is invalid.
 */
export async function readMarketplaceLock(
  root: string,
  diag: DiagnosticCollector,
  onInputRead?: (filePath: string) => void,
): Promise<MarketplaceLock | null> {
  const lockPath = path.join(root, DISCOVERY_MARKETPLACE_LOCK);
  onInputRead?.(lockPath);

  let raw: string;
  try {
    raw = await fs.readFile(lockPath, 'utf-8');
  } catch (err) {
    if ((err as NodeJS.ErrnoException).code === 'ENOENT') {
      diag.info('LOCK_NOT_FOUND', `No marketplace lock file found at ${lockPath}`, {
        filePath: lockPath,
        remediation: 'Run "kb marketplace install <package>" to create one',
      });
      return null;
    }
    diag.error('LOCK_PARSE_ERROR', `Failed to read ${lockPath}: ${(err as Error).message}`, {
      filePath: lockPath,
    });
    return null;
  }

  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch (err) {
    diag.error('LOCK_PARSE_ERROR', `Invalid JSON in ${lockPath}: ${(err as Error).message}`, {
      filePath: lockPath,
      remediation: 'Fix or regenerate the lock file',
    });
    return null;
  }

  if (!isValidLock(parsed)) {
    const isV2 = typeof parsed === 'object' && parsed !== null && 'schema' in parsed && parsed.schema === 'kb.marketplace/2';
    diag.error('LOCK_SCHEMA_INVALID', isV2
      ? 'Lock file uses kb.marketplace/2 and cannot be read by this version.'
      : `Lock file schema mismatch — expected ${SCHEMA_VERSION}`, {
      filePath: lockPath,
      remediation: 'Run "kb marketplace sync" to regenerate .kb/marketplace.lock in schema v3.',
    });
    return null;
  }

  return parsed;
}

// ---------------------------------------------------------------------------
// Write
// ---------------------------------------------------------------------------

/**
 * Write the marketplace lock file atomically (tmp → rename).
 */
export async function writeMarketplaceLock(
  root: string,
  lock: MarketplaceLock,
): Promise<void> {
  const lockPath = path.join(root, DISCOVERY_MARKETPLACE_LOCK);
  const dir = path.dirname(lockPath);
  await fs.mkdir(dir, { recursive: true });

  const tmpPath = `${lockPath}.tmp.${randomUUID()}`;
  const serialized = {
    schema: SCHEMA_VERSION,
    plugins: Object.fromEntries(Object.entries(lock.plugins).sort(([a], [b]) => a < b ? -1 : a > b ? 1 : 0).map(([id, entry]) => [id,
      entry.source === 'local'
        ? { link: entry.link, enabled: entry.enabled ?? true, source: entry.source }
        : entry.source === 'platform'
          ? { version: entry.version, integrity: entry.integrity, resolvedPath: entry.resolvedPath, enabled: entry.enabled ?? true, source: entry.source }
          : { version: entry.version, integrity: entry.integrity, enabled: entry.enabled ?? true, source: entry.source },
    ])),
    ...(lock.adapters && Object.keys(lock.adapters).length > 0 ? {
      adapters: Object.fromEntries(Object.entries(lock.adapters).sort(([a], [b]) => a < b ? -1 : a > b ? 1 : 0).map(([id, entry]) => [id, {
        version: entry.version,
        integrity: entry.integrity,
        resolvedPath: entry.resolvedPath,
        enabled: entry.enabled ?? true,
        source: 'platform',
      }]))
    } : {})
  };
  const content = JSON.stringify(serialized, null, 2) + '\n';

  await fs.writeFile(tmpPath, content, 'utf-8');
  await fs.rename(tmpPath, lockPath);
}

// ---------------------------------------------------------------------------
// Mutate
// ---------------------------------------------------------------------------

/**
 * Add or update an entry in the marketplace lock.
 * Preserves the existing `enabled` flag when the entry already exists —
 * re-installing a disabled package must not silently re-enable it.
 */
export async function addToMarketplaceLock(
  root: string,
  packageId: string,
  entry: MarketplaceEntry,
): Promise<MarketplaceLock> {
  const diag = new DiagnosticCollector();
  const existing = await readMarketplaceLock(root, diag) ?? createEmptyLock();
  const collection = entry.primaryKind === 'adapter' ? (existing.adapters ??= {}) : existing.plugins;
  const existingEntry = collection[packageId];
  collection[packageId] = {
    ...entry,
    // Preserve enabled state from prior install — re-install must not override
    // a user's explicit disable (e.g. parallel test disabling then re-installing).
    enabled: existingEntry?.enabled ?? entry.enabled,
  };
  await writeMarketplaceLock(root, existing);
  return existing;
}

/**
 * Remove an entry from the marketplace lock.
 * Returns true if the entry existed and was removed.
 */
export async function removeFromMarketplaceLock(
  root: string,
  packageId: string,
): Promise<boolean> {
  const diag = new DiagnosticCollector();
  const existing = await readMarketplaceLock(root, diag);
  if (!existing || (!(packageId in existing.plugins) && !(packageId in (existing.adapters ?? {})))) {
    return false;
  }
  delete existing.plugins[packageId];
  if (existing.adapters) {delete existing.adapters[packageId];}
  await writeMarketplaceLock(root, existing);
  return true;
}

// ---------------------------------------------------------------------------
// Factory
// ---------------------------------------------------------------------------

/** Create an empty marketplace lock */
export function createEmptyLock(): MarketplaceLock {
  return { schema: SCHEMA_VERSION, plugins: {} };
}

/** Create a marketplace entry */
export function createMarketplaceEntry(opts: {
  version: string;
  integrity: string;
  resolvedPath?: string;
  source: 'marketplace' | 'local' | 'platform';
  primaryKind: EntityKind;
  provides: EntityKind[];
  signature?: EntitySignature;
  trust?: 'trusted' | 'untrusted';
  spec?: string;
}): MarketplaceEntry {
  return {
    version: opts.version,
    integrity: opts.integrity,
    source: opts.source,
    ...(opts.source === 'local' && opts.resolvedPath ? { link: opts.resolvedPath } : {}),
    ...(opts.source === 'platform' && opts.resolvedPath ? { resolvedPath: opts.resolvedPath } : {}),
    primaryKind: opts.primaryKind,
    provides: opts.provides,
    enabled: true,
    signature: opts.signature,
    trust: opts.trust ?? 'untrusted',
    spec: opts.spec,
  };
}

// ---------------------------------------------------------------------------
// Enable / Disable
// ---------------------------------------------------------------------------

/**
 * Set `enabled` on an entry in the marketplace lock.
 * Returns false if the package is not found in the lock.
 */
async function setPluginEnabled(
  root: string,
  packageId: string,
  enabled: boolean,
): Promise<boolean> {
  const diag = new DiagnosticCollector();
  const existing = await readMarketplaceLock(root, diag);
  if (!existing || (!(packageId in existing.plugins) && !(packageId in (existing.adapters ?? {})))) {
    return false;
  }
  const entry = existing.plugins[packageId] ?? existing.adapters?.[packageId];
  if (entry) {entry.enabled = enabled;}
  await writeMarketplaceLock(root, existing);
  return true;
}

/**
 * Mark a plugin as enabled in the marketplace lock.
 * Returns false if the package is not installed.
 */
export async function enablePlugin(root: string, packageId: string): Promise<boolean> {
  return setPluginEnabled(root, packageId, true);
}

/**
 * Mark a plugin as disabled in the marketplace lock.
 * Returns false if the package is not installed.
 */
export async function disablePlugin(root: string, packageId: string): Promise<boolean> {
  return setPluginEnabled(root, packageId, false);
}

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

function isValidLock(value: unknown): value is MarketplaceLock {
  if (typeof value !== 'object' || value === null) {return false;}
  const obj = value as Record<string, unknown>;
  return (
    Object.keys(obj).every((key) => key === 'schema' || key === 'plugins' || key === 'adapters') &&
    obj.schema === SCHEMA_VERSION &&
    typeof obj.plugins === 'object' &&
    obj.plugins !== null &&
    !Array.isArray(obj.plugins) &&
    (obj.adapters === undefined || (typeof obj.adapters === 'object' && obj.adapters !== null && !Array.isArray(obj.adapters))) &&
    Object.entries(obj.plugins).every(([id, raw]) => {
      if (!/^(?:@[a-z0-9][a-z0-9._-]*\/)?[a-z0-9][a-z0-9._-]*$/i.test(id) || typeof raw !== 'object' || raw === null) {return false;}
      const entry = raw as Record<string, unknown>;
      if (typeof entry.enabled !== 'boolean' || !['marketplace', 'local', 'platform'].includes(String(entry.source))) {return false;}
      const allowed = entry.source === 'marketplace' ? ['version', 'integrity', 'enabled', 'source']
        : entry.source === 'local' ? ['link', 'enabled', 'source']
          : ['version', 'integrity', 'resolvedPath', 'enabled', 'source'];
      if (Object.keys(entry).some((key) => !allowed.includes(key))) {return false;}
      if (entry.source === 'marketplace') {return typeof entry.version === 'string' && validIntegrity(entry.integrity);}
      if (entry.source === 'local') {return typeof entry.link === 'string' && !path.isAbsolute(entry.link);}
      return typeof entry.resolvedPath === 'string' && typeof entry.version === 'string' && validIntegrity(entry.integrity);
    }) &&
    Object.entries((obj.adapters ?? {}) as Record<string, unknown>).every(([id, raw]) => {
      if (!/^(?:@[a-z0-9][a-z0-9._-]*\/)?[a-z0-9][a-z0-9._-]*$/i.test(id) || typeof raw !== 'object' || raw === null) {return false;}
      const entry = raw as Record<string, unknown>;
      return Object.keys(entry).every((key) => ['version', 'integrity', 'resolvedPath', 'enabled', 'source'].includes(key)) &&
        entry.source === 'platform' && typeof entry.version === 'string' && validIntegrity(entry.integrity) &&
        typeof entry.resolvedPath === 'string' && typeof entry.enabled === 'boolean';
    })
  );
}

function validIntegrity(value: unknown): value is string {
  return typeof value === 'string' && /^sha(256|512)-[A-Za-z0-9+/=]+$/.test(value);
}
