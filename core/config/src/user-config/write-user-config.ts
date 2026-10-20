/**
 * @module @kb-labs/core-config/user-config/write-user-config
 *
 * The only writers of user config files (ADR-0047: one writer per file).
 * `setUserConfigValue` and `unsetUserConfigValue` are atomic, locked writes:
 *
 *   1. take the file lock;
 *   2. read the file and check the caller's expected revision;
 *   3. reject unknown keys (with a suggestion), raw secrets and references to
 *      unset secret variables on set;
 *   4. splice or remove the requested JSONC property (comments and formatting kept);
 *   5. validate the resulting document (a write is refused only when it
 *      introduces a NEW schema issue, so a file that is already imperfect
 *      elsewhere can still be edited);
 *   6. write via tmp file + rename, refusing if the file changed underneath.
 *
 * Errors are `ConfigWriteError` carrying a catalog code (`KB_CONFIG_*`); the
 * command layer turns them into the unified error envelope.
 */

import { createHash } from 'node:crypto';
import { promises as fsp } from 'node:fs';
import path from 'node:path';

import type { ZodTypeAny } from 'zod';
import type { DetailsFor, DetailsOptionFor, PlatformErrorCode } from '@kb-labs/core-platform';

import { withFileLock, FileLockTimeoutError, type FileLockOptions } from './file-lock.js';
import {
  JsoncPathError,
  JsoncSyntaxError,
  deleteJsoncValue,
  parseJsonc,
  setJsoncValue,
} from './jsonc-edit.js';
import { flattenZodIssues, resolveKeyPath } from './schema-keys.js';
import { findPlainSecrets, findSecretReferences } from './secrets.js';

export type ConfigWriteErrorCode =
  | 'KB_CONFIG_INVALID'
  | 'KB_CONFIG_UNKNOWN_KEY'
  | 'KB_CONFIG_SECRET_MISSING'
  | 'KB_CONFIG_SECRET_PLAINTEXT'
  | 'KB_CONFIG_CONFLICT'
  | 'KB_ADAPTER_NOT_INSTALLED'
  | 'KB_ADAPTER_SLOT_MISMATCH'
  | 'KB_PLUGIN_SETTINGS_INVALID'
  | 'KB_PLUGIN_SETTINGS_SCHEMA_INVALID';

/** A config write was refused. `details` are catalogue fields; `metadata` stays internal. */
export class ConfigWriteError<C extends ConfigWriteErrorCode = ConfigWriteErrorCode> extends Error {
  readonly code: C;
  readonly details: DetailsFor<C & PlatformErrorCode>;
  readonly metadata: Readonly<Record<string, string>>;

  constructor(code: C, message: string, details: DetailsFor<C & PlatformErrorCode>, metadata: Readonly<Record<string, string>> = {}) {
    super(message);
    this.name = 'ConfigWriteError';
    this.code = code;
    this.details = details;
    this.metadata = metadata;
  }
}

interface ConfigIssueFields {
  /** Dotted path of the offending value. */
  path: string;
  message: string;
  /** Specific next step for semantic validators such as adapter binding. */
  hint?: string;
}

type CataloguedConfigIssue = {
  [C in ConfigWriteErrorCode]: { code: C } & DetailsOptionFor<C & PlatformErrorCode>;
}[ConfigWriteErrorCode];

/** Semantic failures correlate their code with catalogue-declared fields; plain validation issues have no details. */
export type ConfigIssue = ConfigIssueFields & (CataloguedConfigIssue | { code?: undefined; details?: never });

export interface SetUserConfigOptions {
  /** Absolute path of the user config file. Created when missing. */
  filePath: string;
  /** Key path split into segments, for example `['platform', 'adapters', 'llm']`. */
  path: readonly string[];
  /** Value to store (already parsed). */
  value: unknown;
  /** Schema of the whole document; when omitted only syntax is checked. */
  schema?: ZodTypeAny;
  /**
   * Extra semantic validation of the NEW document, for example adapter
   * `configSchema` checks. Return issues; only ones absent before the edit
   * block the write.
   */
  validate?: (document: Record<string, unknown>) => ConfigIssue[] | Promise<ConfigIssue[]>;
  /** Store a raw secret value instead of demanding an `${ENV_VAR}` reference. */
  allowPlainSecret?: boolean;
  /** Accept a `${ENV_VAR}` secret reference whose variable is currently unset. */
  allowMissingEnv?: boolean;
  /** Environment used to check secret references. Defaults to `process.env`. */
  env?: NodeJS.ProcessEnv;
  /** Compute and validate, but do not touch the file. */
  dryRun?: boolean;
  /** Revision (see `revisionOf`) the caller last read; a different file is a conflict. */
  expectedRevision?: string;
  lock?: FileLockOptions;
}

export type UnsetUserConfigOptions = Pick<SetUserConfigOptions,
  'filePath' | 'path' | 'schema' | 'validate' | 'dryRun' | 'expectedRevision' | 'lock'>;

export interface SetUserConfigResult {
  filePath: string;
  /** True when the file did not exist before this write. */
  created: boolean;
  /** False when the value was already stored (no write happens). */
  changed: boolean;
  dryRun: boolean;
  /** Value at the path before the edit (`undefined` when absent). May be a secret: redact before display. */
  previousValue: unknown;
  /** Revision of the file before the edit. */
  revisionBefore: string;
  /** Revision of the file after the edit (equal to `revisionBefore` when unchanged). */
  revisionAfter: string;
  /** Full text the file has (or, for a dry run, would have) after the edit. */
  text: string;
}

/** Content hash used for optimistic concurrency. */
export function revisionOf(text: string): string {
  return createHash('sha256').update(text).digest('hex').slice(0, 16);
}

function dotted(segments: readonly string[]): string {
  return segments.join('.');
}

async function readIfExists(filePath: string): Promise<string | undefined> {
  try {
    return await fsp.readFile(filePath, 'utf8');
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === 'ENOENT') {
      return undefined;
    }
    throw error;
  }
}

function parseDocument(text: string, filePath: string): Record<string, unknown> {
  if (text.trim() === '') {
    return {};
  }
  let doc: unknown;
  try {
    doc = parseJsonc(text);
  } catch (error) {
    if (error instanceof JsoncSyntaxError) {
      throw new ConfigWriteError('KB_CONFIG_INVALID', `${path.basename(filePath)} is not valid JSONC: ${error.message}`, {
        path: path.basename(filePath),
      }, { line: String(error.line) });
    }
    throw error;
  }
  if (doc === null || typeof doc !== 'object' || Array.isArray(doc)) {
    throw new ConfigWriteError('KB_CONFIG_INVALID', `${path.basename(filePath)} must contain a JSON object at the top level`, {
      path: path.basename(filePath),
    });
  }
  return doc as Record<string, unknown>;
}

function schemaIssues(schema: ZodTypeAny | undefined, document: unknown): ConfigIssue[] {
  if (!schema) {
    return [];
  }
  const parsed = schema.safeParse(document);
  if (parsed.success) {
    return [];
  }
  return flattenZodIssues(parsed.error);
}

function issueKey(issue: ConfigIssue): string {
  return `${issue.path}\u0000${issue.message}`;
}

function issueOverlapsWritePath(issue: ConfigIssue, keyPath: readonly string[]): boolean {
  const issuePath = issue.path.split('.');
  const sharedLength = Math.min(issuePath.length, keyPath.length);
  return issuePath.slice(0, sharedLength).every((segment, index) => segment === keyPath[index]);
}

/** Write text atomically (tmp + rename in the same directory), keeping the file mode. */
async function writeAtomicKeepingMode(filePath: string, text: string): Promise<void> {
  await fsp.mkdir(path.dirname(filePath), { recursive: true });
  let mode = 0o644;
  try {
    mode = (await fsp.stat(filePath)).mode & 0o777;
  } catch {
    // New file: default mode.
  }
  const tmp = `${filePath}.tmp-${process.pid}-${Math.random().toString(36).slice(2)}`;
  try {
    const handle = await fsp.open(tmp, 'wx', mode);
    try {
      await handle.writeFile(text);
      await handle.sync();
    } finally {
      await handle.close();
    }
    await fsp.rename(tmp, filePath);
  } catch (error) {
    await fsp.rm(tmp, { force: true });
    throw error;
  }
}

async function computeEdit(options: SetUserConfigOptions, text: string, filePath: string): Promise<string> {
  const { path: keyPath, value, schema } = options;
  const env = options.env ?? process.env;
  const keyName = dotted(keyPath);

  if (keyPath.length === 0 || keyPath.some((segment) => segment === '')) {
    throw new ConfigWriteError('KB_CONFIG_INVALID', 'The key path is empty or has an empty segment', { path: keyName });
  }

  if (schema) {
    const resolution = resolveKeyPath(schema, keyPath);
    if (resolution.status === 'unknown') {
      throw new ConfigWriteError('KB_CONFIG_UNKNOWN_KEY', `Unknown configuration key ${keyName}`, {
        path: keyName,
        suggestion: resolution.suggestion ?? '',
      }, {
        key: resolution.key,
        parent: resolution.parentPath,
        knownKeys: resolution.knownKeys.join(','),
      });
    }
  }

  if (!options.allowPlainSecret) {
    const plain = findPlainSecrets(keyPath, value)[0];
    if (plain) {
      throw new ConfigWriteError(
        'KB_CONFIG_SECRET_PLAINTEXT',
        `Refusing to store a plain secret at ${dotted(plain.path)}`,
        { path: dotted(plain.path), example: `kb config set ${dotted(plain.path)} '\${ENV_VAR}'` },
        { reason: plain.reason },
      );
    }
  }

  if (!options.allowMissingEnv) {
    const missing = findSecretReferences(keyPath, value).find((ref) => env[ref.variable] === undefined);
    if (missing) {
      throw new ConfigWriteError(
        'KB_CONFIG_SECRET_MISSING',
        `${dotted(missing.path)} references ${missing.variable}, which is not set`,
        { variable: missing.variable },
        { path: dotted(missing.path) },
      );
    }
  }

  const before = parseDocument(text, filePath);

  let nextText: string;
  try {
    nextText = setJsoncValue(text, keyPath, value);
  } catch (error) {
    if (error instanceof JsoncPathError) {
      throw new ConfigWriteError('KB_CONFIG_INVALID', error.message, {
        path: dotted(error.blockedAt.length > 0 ? error.blockedAt : keyPath),
      });
    }
    if (error instanceof JsoncSyntaxError) {
      throw new ConfigWriteError('KB_CONFIG_INVALID', error.message, { path: path.basename(filePath) });
    }
    throw error;
  }

  const after = parseDocument(nextText, filePath);

  const knownBefore = new Set(
    [...schemaIssues(schema, before), ...((await options.validate?.(before)) ?? [])].map(issueKey),
  );
  const introduced = [...schemaIssues(schema, after), ...((await options.validate?.(after)) ?? [])].filter(
    (issue) => issueOverlapsWritePath(issue, keyPath) || !knownBefore.has(issueKey(issue)),
  );
  const first = introduced[0];
  if (first) {
    throw new ConfigWriteError(first.code ?? 'KB_CONFIG_INVALID', `${first.path || keyName}: ${first.message}`, {
      path: first.path || keyName,
      ...(first.code === 'KB_CONFIG_INVALID' ? {} : first.details ?? {}),
    }, { ...(first.details ?? {}), ...(first.hint ? { hint: first.hint } : {}) });
  }

  return nextText;
}

async function computeUnset(options: UnsetUserConfigOptions, text: string, filePath: string): Promise<string> {
  const { path: keyPath, schema } = options;
  const keyName = dotted(keyPath);
  if (keyPath.length === 0 || keyPath.some((segment) => segment === '')) {
    throw new ConfigWriteError('KB_CONFIG_INVALID', 'The key path is empty or has an empty segment', { path: keyName });
  }
  if (schema) {
    const resolution = resolveKeyPath(schema, keyPath);
    if (resolution.status === 'unknown') {
      throw new ConfigWriteError('KB_CONFIG_UNKNOWN_KEY', `Unknown configuration key ${keyName}`, {
        path: keyName,
        suggestion: resolution.suggestion ?? '',
      }, {
        key: resolution.key,
        parent: resolution.parentPath,
        knownKeys: resolution.knownKeys.join(','),
      });
    }
  }

  const before = parseDocument(text, filePath);
  const nextText = deleteJsoncValue(text, keyPath);
  const after = parseDocument(nextText, filePath);
  const knownBefore = new Set(
    [...schemaIssues(schema, before), ...((await options.validate?.(before)) ?? [])].map(issueKey),
  );
  const introduced = [...schemaIssues(schema, after), ...((await options.validate?.(after)) ?? [])].filter(
    (issue) => !knownBefore.has(issueKey(issue)),
  );
  const first = introduced[0];
  if (first) {
    throw new ConfigWriteError('KB_CONFIG_INVALID', `${first.path || keyName}: ${first.message}`, {
      path: first.path || keyName,
    });
  }
  return nextText;
}

/**
 * Set one key in the user config file. See the module docs for the guarantees.
 * Throws `ConfigWriteError`.
 */
export async function setUserConfigValue(options: SetUserConfigOptions): Promise<SetUserConfigResult> {
  let filePath = path.resolve(options.filePath);
  try {
    filePath = await fsp.realpath(filePath);
  } catch {
    // Missing file: keep the resolved path.
  }

  const run = async (): Promise<SetUserConfigResult> => {
    const existing = await readIfExists(filePath);
    const text = existing ?? '';
    const revisionBefore = revisionOf(text);

    if (options.expectedRevision !== undefined && options.expectedRevision !== revisionBefore) {
      throw new ConfigWriteError('KB_CONFIG_CONFLICT', `${filePath} changed since it was read`, {
        path: filePath,
      });
    }

    const previousDoc = parseDocument(text, filePath);
    let previousValue: unknown = previousDoc;
    for (const segment of options.path) {
      previousValue =
        previousValue !== null && typeof previousValue === 'object' && Object.prototype.hasOwnProperty.call(previousValue, segment)
          ? (previousValue as Record<string, unknown>)[segment]
          : undefined;
    }

    const nextText = await computeEdit(options, text, filePath);
    const changed = nextText !== text;
    const base: Omit<SetUserConfigResult, 'revisionAfter'> = {
      filePath,
      created: existing === undefined,
      changed,
      dryRun: Boolean(options.dryRun),
      previousValue,
      revisionBefore,
      text: nextText,
    };

    if (!changed || options.dryRun) {
      return { ...base, revisionAfter: changed ? revisionOf(nextText) : revisionBefore };
    }

    // The lock only serialises cooperating writers. Re-check right before the
    // rename so an unlocked external edit is reported instead of overwritten.
    const current = (await readIfExists(filePath)) ?? '';
    if (revisionOf(current) !== revisionBefore) {
      throw new ConfigWriteError('KB_CONFIG_CONFLICT', `${filePath} changed while the edit was prepared`, {
        path: filePath,
      });
    }
    await writeAtomicKeepingMode(filePath, nextText);
    return { ...base, revisionAfter: revisionOf(nextText) };
  };

  if (options.dryRun) {
    return run();
  }
  try {
    return await withFileLock(filePath, run, options.lock);
  } catch (error) {
    if (error instanceof FileLockTimeoutError) {
      throw new ConfigWriteError('KB_CONFIG_CONFLICT', `${filePath} is locked by another writer`, { path: filePath });
    }
    throw error;
  }
}

/** Remove one key from a user config file with the same locking and atomic-write guarantees as set. */
export async function unsetUserConfigValue(options: UnsetUserConfigOptions): Promise<SetUserConfigResult> {
  let filePath = path.resolve(options.filePath);
  try {
    filePath = await fsp.realpath(filePath);
  } catch {
    // Missing file: retain the resolved path and report an unchanged dry operation.
  }

  const run = async (): Promise<SetUserConfigResult> => {
    const existing = await readIfExists(filePath);
    const text = existing ?? '';
    const revisionBefore = revisionOf(text);
    if (options.expectedRevision !== undefined && options.expectedRevision !== revisionBefore) {
      throw new ConfigWriteError('KB_CONFIG_CONFLICT', `${filePath} changed since it was read`, { path: filePath });
    }

    const previousDoc = parseDocument(text, filePath);
    let previousValue: unknown = previousDoc;
    for (const segment of options.path) {
      previousValue = previousValue !== null && typeof previousValue === 'object'
        && Object.prototype.hasOwnProperty.call(previousValue, segment)
        ? (previousValue as Record<string, unknown>)[segment]
        : undefined;
    }
    const editedText = await computeUnset(options, text, filePath);
    const nextText = previousValue === undefined ? text : editedText;
    const changed = nextText !== text;
    const base: Omit<SetUserConfigResult, 'revisionAfter'> = {
      filePath,
      created: false,
      changed,
      dryRun: Boolean(options.dryRun),
      previousValue,
      revisionBefore,
      text: nextText,
    };
    if (!changed || options.dryRun) {
      return { ...base, revisionAfter: changed ? revisionOf(nextText) : revisionBefore };
    }

    const current = (await readIfExists(filePath)) ?? '';
    if (revisionOf(current) !== revisionBefore) {
      throw new ConfigWriteError('KB_CONFIG_CONFLICT', `${filePath} changed while the edit was prepared`, { path: filePath });
    }
    await writeAtomicKeepingMode(filePath, nextText);
    return { ...base, revisionAfter: revisionOf(nextText) };
  };

  if (options.dryRun) {return run();}
  try {
    return await withFileLock(filePath, run, options.lock);
  } catch (error) {
    if (error instanceof FileLockTimeoutError) {
      throw new ConfigWriteError('KB_CONFIG_CONFLICT', `${filePath} is locked by another writer`, { path: filePath });
    }
    throw error;
  }
}
