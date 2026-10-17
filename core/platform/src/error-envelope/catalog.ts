/**
 * @module @kb-labs/core-platform/error-envelope/catalog
 *
 * Typed access to `errors.catalog.json` (the data source for platform error
 * codes) and the helper that builds an envelope from a catalog code.
 */

import catalogData from './errors.catalog.json';
import { markValidatedErrorEnvelope, type ValidatedErrorEnvelope } from './validated-envelope.js';
import type {
  ErrorAction,
  ErrorCatalog,
  ErrorCatalogEntry,
  ErrorCode,
  PlatformErrorCode,
  ErrorEnvelope,
  PlatformDiagnostic,
} from './types.js';
import { catalogRequiredTemplateParameters, catalogStructuredDetailFields, catalogTemplateParameterNames } from './error-codes.generated.js';
import type { CatalogRuntimeTemplateParameters, CatalogTemplateParameters } from './error-codes.generated.js';
import type { CatalogDetailsFor } from './error-codes.generated.js';

/** Catalogue-derived detail fields accepted by a bridge for a specific code. */
export type DetailsFor<C extends PlatformErrorCode> = C extends PlatformErrorCode ? CatalogDetailsFor[C] : never;

type RequiredDetailKeys<T> = { [K in keyof T]-?: {} extends Pick<T, K> ? never : K }[keyof T];

/** Positional detail argument for typed bridges; required catalogue fields cannot be omitted. */
export type DetailsArgumentFor<C extends PlatformErrorCode> = [RequiredDetailKeys<DetailsFor<C>>] extends [never]
  ? [details?: DetailsFor<C>]
  : [details: DetailsFor<C>];

/** Object detail property for typed bridges; required catalogue fields cannot be omitted. */
export type DetailsOptionFor<C extends PlatformErrorCode> = [RequiredDetailKeys<DetailsFor<C>>] extends [never]
  ? { details?: DetailsFor<C> }
  : { details: DetailsFor<C> };

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

/** Substitutes only catalogue-declared names; all other brace text remains literal. */
export function renderTemplate(
  template: string,
  values: Readonly<Record<string, string>> = {},
  declaredNames: readonly string[] = [],
): string {
  const declared = new Set(declaredNames);
  let rendered = '';
  let remaining = template;
  while (remaining.length > 0) {
    const opening = remaining.indexOf('{');
    if (opening < 0) return rendered + remaining;
    rendered += remaining.slice(0, opening);
    const closing = remaining.indexOf('}', opening + 1);
    if (closing < 0) return rendered + remaining.slice(opening);
    const name = remaining.slice(opening + 1, closing);
    const literal = remaining.slice(opening, closing + 1);
    rendered += declared.has(name) && values[name] !== undefined ? values[name] : literal;
    remaining = remaining.slice(closing + 1);
  }
  return rendered;
}

type DataTemplateParameters<C extends PlatformErrorCode> = CatalogTemplateParameters[C];
type RuntimeTemplateParameters<C extends PlatformErrorCode> = CatalogRuntimeTemplateParameters[C];
type TemplateValueOption<C extends PlatformErrorCode> =
  [RequiredDetailKeys<RuntimeTemplateParameters<C>>] extends [never]
    ? { templateValues?: RuntimeTemplateParameters<C> }
    : { templateValues: RuntimeTemplateParameters<C> };
type CreateEnvelopeOptionsFor<C extends PlatformErrorCode> =
  EnvelopeOptionFields & DetailsOptionFor<C> & TemplateValueOption<C>;

export type CreateEnvelopeOptions<C extends PlatformErrorCode = never> = EnvelopeOptionFields
  & DetailsOptionFor<C>
  & TemplateValueOption<C>;

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

type EnvelopeOptionsArgs<C extends PlatformErrorCode> = [RequiredDetailKeys<DetailsFor<C>>] extends [never]
  ? [RequiredDetailKeys<RuntimeTemplateParameters<C>>] extends [never] ? [options?: CreateEnvelopeOptionsFor<C>] : [options: CreateEnvelopeOptionsFor<C>]
  : [options: CreateEnvelopeOptionsFor<C>];

/** Builds an envelope from a catalog code. Throws for missing template values or an unknown code. */
export function createErrorEnvelope<C extends PlatformErrorCode>(code: C, ...args: EnvelopeOptionsArgs<C>): ValidatedErrorEnvelope;
export function createErrorEnvelope(code: PlatformErrorCode, options: EnvelopeOptionFields & { details?: unknown; templateValues?: unknown } = {}): ValidatedErrorEnvelope {
  return buildErrorEnvelope(code, options);
}

/** Dynamic error bridge. Unknown codes and detail maps are validated before an envelope is built. */
export function createValidatedErrorEnvelope(
  code: unknown,
  options: EnvelopeOptionFields & { details?: unknown; templateValues?: unknown } = {},
): ValidatedErrorEnvelope {
  if (typeof code !== 'string' || !isErrorCode(code)) {
    return createErrorEnvelope('KB_RUNTIME_UNEXPECTED');
  }
  return buildErrorEnvelope(code, options);
}

function toStringMap(value: unknown): Record<string, string> | undefined | null {
  if (value === undefined) return undefined;
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return null;
  const result: Record<string, string> = {};
  for (const [key, item] of Object.entries(value)) {
    if (typeof item !== 'string') return null;
    result[key] = item;
  }
  return result;
}

function buildErrorEnvelope(code: PlatformErrorCode, options: EnvelopeOptionFields & { details?: unknown; templateValues?: unknown }): ValidatedErrorEnvelope {
  const entry = byCode.get(code);
  if (!entry) {
    throw new Error(`Unknown error code "${code}": add it to errors.catalog.json`);
  }
  const parsedDetails = toStringMap(options.details);
  const parsedTemplateValues = toStringMap(options.templateValues);
  if (parsedDetails === null || parsedTemplateValues === null) {
    throw createErrorDetailsContractViolation('details', 'must be a flat map of string identifiers');
  }
  const details = parsedDetails ?? {};
  const templateValuesInput = parsedTemplateValues ?? {};
  for (const [key, value] of Object.entries(details)) {
    if (typeof value !== 'string') {
      throw createErrorDetailsContractViolation(key, 'must be a string identifier');
    }
    if (isSerializedStructuredValue(value)) {
      throw createErrorDetailsContractViolation(key, 'contains serialized JSON');
    }
  }
  const parameterNames = catalogTemplateParameterNames[code];
  const allowedDetails = new Set([...parameterNames, ...catalogStructuredDetailFields[code]]);
  const templateValues: Record<string, string> = {
    ...details,
    ...templateValuesInput,
    ...(options.correlationId ? { correlationId: options.correlationId } : {}),
  };
  const violationFields = [
    ...Object.keys(details).filter((key) => !allowedDetails.has(key)),
    ...Object.keys(templateValuesInput).filter((key) => !parameterNames.includes(key)),
  ];
  for (const name of catalogRequiredTemplateParameters[code]) {
    if (templateValues[name] === undefined || templateValues[name].trim() === '') violationFields.push(name);
  }
  if (violationFields.length > 0) {
    return createDiagnosticContractViolation(code, [...new Set(violationFields)].sort());
  }
  const envelope: ErrorEnvelope = {
    code: entry.code,
    area: entry.area,
    stage: entry.stage,
    severity: options.severity ?? entry.severity,
    retryable: entry.retryable,
    message: options.message ?? renderTemplate(entry.message, templateValues, parameterNames),
    hint: options.hint ?? renderTemplate(
      !options.correlationId && entry.hintWithoutCorrelationId ? entry.hintWithoutCorrelationId : entry.hint,
      templateValues,
      parameterNames,
    ),
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
  return markValidatedErrorEnvelope(envelope);
}

function createDiagnosticContractViolation(originalCode: string, fields: readonly string[]): ValidatedErrorEnvelope {
  return createErrorEnvelope('KB_RUNTIME_DIAGNOSTIC_CONTRACT_VIOLATION', {
    details: { originalCode, fields: fields.join(', ') },
  });
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
