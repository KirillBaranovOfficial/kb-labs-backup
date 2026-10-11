/**
 * @module @kb-labs/core-platform/error-envelope/catalog
 *
 * Typed access to `errors.catalog.json` (the data source for platform error
 * codes) and the helper that builds an envelope from a catalog code.
 */

import catalogData from './errors.catalog.json';
import type {
  ErrorAction,
  ErrorCatalog,
  ErrorCatalogEntry,
  ErrorCode,
  PlatformErrorCode,
  ErrorEnvelope,
  PlatformDiagnostic,
} from './types.js';

// The JSON is validated structurally by the catalog lint test; the cast keeps
// the literal union types (area, severity) that JSON inference widens to string.
export const errorCatalog = catalogData as unknown as ErrorCatalog;

const byCode = new Map<string, ErrorCatalogEntry>(
  errorCatalog.codes.map((entry) => [entry.code, entry]),
);

export function isErrorCode(code: string): code is PlatformErrorCode {
  return byCode.has(code);
}

export function getCatalogEntry(code: PlatformErrorCode): ErrorCatalogEntry | undefined {
  return byCode.get(code);
}

/** Map catalogued command failures to the CLI usage exit code at shared boundaries. */
export function mapErrorEnvelopeToExitCode(code: ErrorCode): number {
  return code === 'KB_RUNTIME_INPUT_INVALID' ? 2 : 1;
}

/** Fills `{name}` placeholders from `values`; a missing value renders as "unknown". */
export function renderTemplate(template: string, values: Readonly<Record<string, string>> = {}): string {
  return template.replace(/\{([A-Za-z0-9_]+)\}/g, (_match, key: string) => values[key] ?? 'unknown');
}

export interface CreateEnvelopeOptions {
  /** Template values and structured details (must be secret-free). */
  details?: Record<string, string>;
  cause?: string;
  correlationId?: string;
  actions?: ErrorAction[];
  docs?: string;
  /** Override a catalog hint when a typed error carries a more specific next step. */
  hint?: string;
  /** Override a catalog message when a typed error carries a more specific explanation. */
  message?: string;
  diagnostics?: PlatformDiagnostic[];
  severity?: ErrorEnvelope['severity'];
}

/** Builds an envelope from a catalog code. Throws for a code that is not in the catalog. */
export function createErrorEnvelope(code: PlatformErrorCode, options: CreateEnvelopeOptions = {}): ErrorEnvelope {
  const entry = byCode.get(code);
  if (!entry) {
    throw new Error(`Unknown error code "${code}": add it to errors.catalog.json`);
  }
  const details = options.details ?? {};
  if (typeof details !== 'object' || details === null || Array.isArray(details)) {
    throw createErrorDetailsContractViolation('details', 'must be a flat map of string identifiers');
  }
  for (const [key, value] of Object.entries(details)) {
    if (typeof value !== 'string') {
      throw createErrorDetailsContractViolation(key, 'must be a string identifier');
    }
    if (isSerializedStructuredValue(value)) {
      throw createErrorDetailsContractViolation(key, 'contains serialized JSON');
    }
  }
  const envelope: ErrorEnvelope = {
    code: entry.code,
    area: entry.area,
    stage: entry.stage,
    severity: options.severity ?? entry.severity,
    retryable: entry.retryable,
    message: options.message ?? renderTemplate(entry.message, details),
    hint: options.hint ?? renderTemplate(entry.hint, details),
  };
  if (options.cause && options.cause !== envelope.message) {
    envelope.cause = options.cause;
  }
  if (options.actions && options.actions.length > 0) {
    envelope.actions = options.actions;
  }
  if (options.docs) {
    envelope.docs = options.docs;
  }
  if (options.correlationId) {
    envelope.correlationId = options.correlationId;
  }
  if (Object.keys(details).length > 0) {
    envelope.details = details;
  }
  if (options.diagnostics?.length) {
    envelope.diagnostics = options.diagnostics;
  }
  return envelope;
}

export function isSerializedStructuredValue(value: string): boolean {
  try {
    const parsed: unknown = JSON.parse(value);
    return typeof parsed === 'object' && parsed !== null;
  } catch {
    return false;
  }
}

export function createErrorDetailsContractViolation(key: string, reason: string): Error & { code: string; hint: string } {
  return Object.assign(new Error(`Error detail "${key}" ${reason}; use result data or error.diagnostics.`), {
    code: 'KB_PLUGIN_CONTRACT_VIOLATION',
    hint: 'Keep error.details values as identifiers; return structured reports in data or gate findings in error.diagnostics.',
  });
}
