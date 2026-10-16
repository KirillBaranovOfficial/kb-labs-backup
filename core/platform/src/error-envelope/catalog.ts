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
import type { CatalogHintTemplateParameters, CatalogMessageTemplateParameters, CatalogRuntimeTemplateParameters, CatalogTemplateParameters } from './error-codes.generated.js';

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
  return code === 'KB_RUNTIME_INPUT_INVALID' || code === 'KB_RUNTIME_UNKNOWN_COMMAND' ? 2 : 1;
}

/** Fills `{name}` placeholders from `values`; an unresolved catalog parameter is a defect. */
export function renderTemplate(template: string, values: Readonly<Record<string, string>> = {}): string {
  return template.replace(/\{([A-Za-z0-9_]+)\}/g, (_match, key: string) => {
    const value = values[key];
    if (value === undefined) throw new Error(`unrendered template parameter: ${key}`);
    return value;
  });
}

type DataTemplateParameters<C extends PlatformErrorCode> = CatalogTemplateParameters[C];
type RuntimeTemplateParameters<C extends PlatformErrorCode> = CatalogRuntimeTemplateParameters[C];
type RequiredKeys<T> = { [K in keyof T]-?: {} extends Pick<T, K> ? never : K }[keyof T];
type MessageTemplateParameters<C extends PlatformErrorCode> = CatalogMessageTemplateParameters[C];
type HintTemplateParameters<C extends PlatformErrorCode> = CatalogHintTemplateParameters[C];
type RequiredDataParameters<C extends PlatformErrorCode, O extends EnvelopeOptionFields> =
  (O extends { message: string } ? Record<never, never> : MessageTemplateParameters<C>)
  & (O extends { hint: string } ? Record<never, never> : HintTemplateParameters<C>);
type RequiredHintRuntimeParameters<C extends PlatformErrorCode, O extends EnvelopeOptionFields> =
  O extends { hint: string } ? Record<never, never> : RuntimeTemplateParameters<C>;
type TemplateValueOption<C extends PlatformErrorCode, O extends EnvelopeOptionFields> =
  [RequiredKeys<RequiredHintRuntimeParameters<C, O>>] extends [never]
    ? { templateValues?: RuntimeTemplateParameters<C> }
    : { templateValues: RuntimeTemplateParameters<C> };
type DetailsOption<C extends PlatformErrorCode, O extends EnvelopeOptionFields> =
  [keyof RequiredDataParameters<C, O>] extends [never]
    ? { details?: Record<string, string> }
    : { details: Record<string, string> & RequiredDataParameters<C, O> };
type CreateEnvelopeOptionsFor<C extends PlatformErrorCode, O extends EnvelopeOptionFields> =
  O & DetailsOption<C, O> & TemplateValueOption<C, O>;

export type CreateEnvelopeOptions<C extends PlatformErrorCode = never> = EnvelopeOptionFields
  & DetailsOption<C, EnvelopeOptionFields>
  & TemplateValueOption<C, EnvelopeOptionFields>;

interface EnvelopeOptionFields {
  /** Structured details and invocation-owned template values must be secret-free. */
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

type EnvelopeOptionsArgs<C extends PlatformErrorCode, O extends EnvelopeOptionFields> = [keyof RequiredDataParameters<C, O>] extends [never]
  ? [RequiredKeys<RequiredHintRuntimeParameters<C, O>>] extends [never] ? [options?: CreateEnvelopeOptionsFor<C, O>] : [options: CreateEnvelopeOptionsFor<C, O>]
  : [options: CreateEnvelopeOptionsFor<C, O>];

/** Builds an envelope from a catalog code. Throws for missing template values or an unknown code. */
export function createErrorEnvelope<C extends PlatformErrorCode, O extends EnvelopeOptionFields = EnvelopeOptionFields>(code: C, ...args: EnvelopeOptionsArgs<C, O>): ErrorEnvelope;
export function createErrorEnvelope(code: PlatformErrorCode, options: EnvelopeOptionFields & { details?: Record<string, string>; templateValues?: Readonly<Record<string, string>> } = {}): ErrorEnvelope {
  const entry = byCode.get(code);
  if (!entry) {
    throw new Error(`Unknown error code "${code}": add it to errors.catalog.json`);
  }
  const details = options.details ?? {};
  const templateValues = { ...details, ...options.templateValues };
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
    message: options.message ?? renderTemplate(entry.message, templateValues),
    hint: options.hint ?? renderTemplate(!options.correlationId && entry.hintWithoutCorrelationId
      ? entry.hintWithoutCorrelationId
      : entry.hint, {
      ...templateValues,
      ...(options.correlationId ? { correlationId: options.correlationId } : {}),
    }),
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
