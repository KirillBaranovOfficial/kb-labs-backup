/**
 * @module @kb-labs/core-extension-store/entry
 * Read/validate `entry.json` (ADR-0048 D2). Never repairs a bad file — the same
 * discipline as the project registry (ADR-0044 §5).
 */

import * as fs from 'node:fs/promises';
import { isManifestV3 } from '@kb-labs/plugin-contracts';
import { STORE_ENTRY_SCHEMA, type StoreEntry, type StoreEntryKind } from './types.js';

const KINDS: readonly StoreEntryKind[] = ['plugin', 'adapter'];

function isObject(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

/** Parses and validates raw `entry.json` content. Throws a descriptive error on any structural problem. */
export function parseStoreEntry(raw: string, filePath: string): StoreEntry {
  let data: unknown;
  try {
    data = JSON.parse(raw);
  } catch (err) {
    throw new Error(`${filePath} is not valid JSON: ${(err as Error).message}`);
  }
  if (!isObject(data)) {
    throw new Error(`${filePath}: top-level value is not an object`);
  }
  const { schema, name, version, kind, integrity, sdkApiVersion, manifest, signature, installedAt } = data;

  if (schema !== STORE_ENTRY_SCHEMA) {
    throw new Error(`${filePath}: unsupported schema "${String(schema)}", expected "${STORE_ENTRY_SCHEMA}"`);
  }
  if (typeof name !== 'string' || name.length === 0) {
    throw new Error(`${filePath}: "name" is missing or not a string`);
  }
  if (typeof version !== 'string' || version.length === 0) {
    throw new Error(`${filePath}: "version" is missing or not a string`);
  }
  if (typeof kind !== 'string' || !KINDS.includes(kind as StoreEntryKind)) {
    throw new Error(`${filePath}: "kind" must be one of ${KINDS.join(', ')}`);
  }
  if (typeof integrity !== 'string' || integrity.length === 0) {
    throw new Error(`${filePath}: "integrity" is missing or not a string`);
  }
  if (sdkApiVersion !== undefined && typeof sdkApiVersion !== 'string') {
    throw new Error(`${filePath}: "sdkApiVersion" must be a string when present`);
  }
  if (manifest !== undefined && !isManifestV3(manifest)) {
    throw new Error(`${filePath}: "manifest" is present but is not a valid ManifestV3`);
  }
  if (typeof installedAt !== 'string' || Number.isNaN(Date.parse(installedAt))) {
    throw new Error(`${filePath}: "installedAt" is missing or not a timestamp`);
  }

  return {
    schema: STORE_ENTRY_SCHEMA,
    name,
    version,
    kind: kind as StoreEntryKind,
    integrity,
    sdkApiVersion,
    manifest,
    signature: signature as StoreEntry['signature'],
    installedAt,
  };
}

/** Reads and validates `<entryDir>/entry.json`. Returns `undefined` when the file does not exist. */
export async function readStoreEntry(entryJsonPath: string): Promise<StoreEntry | undefined> {
  let raw: string;
  try {
    raw = await fs.readFile(entryJsonPath, 'utf-8');
  } catch (err) {
    if ((err as NodeJS.ErrnoException).code === 'ENOENT') {return undefined;}
    throw err;
  }
  return parseStoreEntry(raw, entryJsonPath);
}

/** Serializes `entry.json` content, stable key order so it stays diffable if ever inspected. */
export function serializeStoreEntry(entry: StoreEntry): string {
  return `${JSON.stringify(entry, null, 2)}\n`;
}
