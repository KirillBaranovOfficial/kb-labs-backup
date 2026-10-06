/**
 * @module @kb-labs/core-platform/error-envelope/from-error
 *
 * Adapter from platform-level error classes to the unified envelope.
 * Errors defined in higher layers (command-kit's `PluginError`,
 * `ValidationError`, `ServiceNotConfiguredError`) are adapted by
 * `@kb-labs/shared-command-kit`, and plugin-contracts errors by
 * `@kb-labs/plugin-contracts`; both delegate here for the rest.
 */

import { AdapterConfigError, AdapterCredentialsMissingError, AdapterOperationError, AdapterProjectScopeError, AdapterUnavailableError, PlatformConfigError, ServicePortConfigError } from '../errors.js';
import { HostServiceAddressError } from '../host-service-address.js';
import { createErrorDetailsContractViolation, createErrorEnvelope, getCatalogEntry, isSerializedStructuredValue, type CreateEnvelopeOptions } from './catalog.js';
import { ErrorEnvelopeError, type ErrorEnvelope } from './types.js';

export type ToEnvelopeOptions = Pick<CreateEnvelopeOptions, 'correlationId' | 'actions' | 'docs'>;

/** Convert any thrown value to an envelope. Unknown values become `KB_RUNTIME_UNEXPECTED`. */
export function toErrorEnvelope(error: unknown, options: ToEnvelopeOptions = {}): ErrorEnvelope {
  if (error instanceof ErrorEnvelopeError) {
    return error.envelope;
  }
  if (error instanceof AdapterConfigError) {
    return createErrorEnvelope('KB_CONFIG_INVALID', {
      ...options,
      details: { path: error.optionPath },
      cause: error.detail,
      message: error.message,
      hint: error.hint,
    });
  }
  if (error instanceof ServicePortConfigError) {
    return createErrorEnvelope('KB_CONFIG_INVALID', {
      ...options,
      details: { path: error.envVar },
      cause: error.message,
      message: error.message,
      hint: `Run "unset ${error.envVar}", then retry the command to use the service default port.`,
    });
  }
  if (error instanceof HostServiceAddressError) {
    return createErrorEnvelope(
      error.reason === 'missing' ? 'KB_HOST_SERVICE_ADDRESS_MISSING' : 'KB_HOST_SERVICE_ADDRESS_INVALID',
      {
        ...options,
        cause: error.message,
        details: { service: error.service },
        hint: error.hint,
      },
    );
  }
  if (error instanceof PlatformConfigError) {
    const message = error.reason === 'read'
      ? `Platform configuration file "${error.configPath}" cannot be read.`
      : error.reason === 'parse'
        ? `Platform configuration file "${error.configPath}" contains invalid JSON.`
        : `Platform configuration file "${error.configPath}" is invalid.`;
    const hint = error.reason === 'parse'
      ? `Run python3 -m json.tool "${error.configPath}" to locate the syntax error, fix the file, then retry the CLI command.`
      : error.reason === 'read'
        ? `Run ls -l "${error.configPath}" to inspect the file and permissions, correct the issue, then retry the CLI command.`
        : `Run sed -n '1,200p' "${error.configPath}" to inspect the invalid settings, correct the reported issue, then retry the CLI command.`;
    return createErrorEnvelope('KB_CONFIG_INVALID', {
      ...options,
      details: { path: error.configPath },
      cause: error.detail,
      message,
      hint,
    });
  }
  if (error instanceof AdapterProjectScopeError) {
    return createErrorEnvelope('KB_ADAPTER_PROJECT_SCOPE', {
      ...options,
      details: { path: 'platform.adapters / platform.adapterOptions' },
      cause: error.message,
    });
  }
  if (error instanceof AdapterCredentialsMissingError) {
    const envReference = '${' + error.credential + '}';
    const command = `kb config set --scope platform ${error.configurationPath} '${envReference}' --allow-missing-env`;
    return createErrorEnvelope('KB_ADAPTER_CREDENTIALS_MISSING', {
      ...options,
      cause: error.message,
      details: {
        service: error.slot,
        credential: error.credential,
        path: error.configurationPath,
        command,
      },
      actions: [{ id: 'configure-adapter-credential', label: 'Configure adapter credential', command }],
    });
  }
  if (error instanceof AdapterUnavailableError) {
    if (error.reason === 'load-failed') {
      return createErrorEnvelope('KB_HOST_ADAPTER_UNAVAILABLE', {
        ...options,
        cause: error.message,
        details: { slot: error.slot, reason: error.reason },
        ...(error.hint ? { hint: error.hint } : {}),
        ...(error.userMessage ? { message: error.userMessage } : {}),
        ...(error.action ? { actions: [error.action] } : {}),
      });
    }
    const command = getAdapterConfigurationCommand(error.slot);
    return createErrorEnvelope('KB_ADAPTER_NOT_CONFIGURED', {
      ...options,
      cause: error.message,
      details: { service: error.slot, adapter: `platform.adapters.${error.slot}`, command },
      actions: [{
        id: 'configure-adapter',
        label: `Configure ${error.slot}`,
        command,
      }],
    });
  }
  if (error instanceof AdapterOperationError) {
    return createErrorEnvelope('KB_ADAPTER_OPERATION_FAILED', {
      ...options,
      cause: error.message,
      details: { slot: error.slot, operation: error.operation },
      hint: `Run "kb logs query" to inspect the ${error.slot} adapter failure, correct the underlying issue, then retry the operation.`,
    });
  }
  const source = getErrorRecord(error);
  const code = typeof source?.code === 'string' && getCatalogEntry(source.code)
    ? source.code
    : 'KB_RUNTIME_UNEXPECTED';
  const details = getDetails(source?.details);
  if (details === null) {
    return createErrorEnvelope('KB_PLUGIN_CONTRACT_VIOLATION', {
      message: 'Error details must be a flat map of string identifiers.',
      hint: 'Keep error.details values as identifiers; return structured reports in data or gate findings in error.diagnostics.',
    });
  }

  const correlationId = typeof source?.correlationId === 'string'
    ? source.correlationId
    : options.correlationId ?? (code === 'KB_RUNTIME_UNEXPECTED' ? globalThis.crypto.randomUUID() : undefined);
  const cause = code === 'KB_RUNTIME_UNEXPECTED'
    ? typeof error === 'string' ? error : undefined
    : typeof source?.cause === 'string'
    ? source.cause
    : error instanceof Error
      ? error.message
      : typeof error === 'string'
        ? error
        : undefined;
  const message = code !== 'KB_RUNTIME_UNEXPECTED' && typeof source?.message === 'string'
    ? source.message
    : undefined;
  const hint = code !== 'KB_RUNTIME_UNEXPECTED' && typeof source?.hint === 'string'
    ? source.hint
    : undefined;
  const diagnostics = Array.isArray(source?.diagnostics)
    ? source.diagnostics as ErrorEnvelope['diagnostics']
    : undefined;

  try {
    return createErrorEnvelope(code, {
      ...options,
      ...(cause ? { cause } : {}),
      ...(message ? { message } : {}),
      ...(hint ? { hint } : {}),
      ...(details && Object.keys(details).length > 0 ? { details } : {}),
      ...(diagnostics ? { diagnostics } : {}),
      ...(correlationId ? { correlationId } : {}),
    });
  } catch (error) {
    if (isContractViolation(error)) {
      return createErrorEnvelope('KB_PLUGIN_CONTRACT_VIOLATION', {
        message: error.message,
        hint: error.hint,
      });
    }
    throw error;
  }
}

type ErrorRecord = Record<string, unknown>;

function getErrorRecord(error: unknown): ErrorRecord | undefined {
  if (error instanceof Error) {
    return error as Error & ErrorRecord;
  }
  if (typeof error !== 'object' || error === null) {
    return undefined;
  }
  if ('error' in error && typeof error.error === 'object' && error.error !== null) {
    return error.error as ErrorRecord;
  }
  return error as ErrorRecord;
}

function getDetails(value: unknown): Record<string, string> | null | undefined {
  if (value === undefined) return undefined;
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return null;
  const entries = Object.entries(value);
  if (entries.some(([key, item]) => typeof item !== 'string' || isSerializedStructuredValue(item))) {
    return null;
  }
  return Object.fromEntries(entries) as Record<string, string>;
}

function isContractViolation(error: unknown): error is Error & { code: string; hint: string } {
  return error instanceof Error && 'code' in error && error.code === 'KB_PLUGIN_CONTRACT_VIOLATION'
    && 'hint' in error && typeof error.hint === 'string';
}

function getAdapterConfigurationCommand(slot: string): string {
  const packages: Record<string, string> = {
    llm: '@kb-labs/adapters-openai',
    embeddings: '@kb-labs/adapters-openai/embeddings',
    vectorStore: '@kb-labs/adapters-qdrant',
  };
  const adapterPackage = packages[slot] ?? `@kb-labs/adapters-${slot.replace(/[A-Z]/g, (letter) => `-${letter.toLowerCase()}`)}`;
  return `kb adapter bind ${slot} ${adapterPackage}`;
}

/** Statuses (HTTP-style) that a caller can reasonably retry. */
const RETRYABLE_STATUS = new Set([408, 425, 429, 500, 502, 503, 504]);

export const PRODUCT_FALLBACK_HINT =
  'Run the command again with --debug for details; if it persists, include the correlationId in a report.';

export function isRetryableStatus(statusCode: number): boolean {
  return RETRYABLE_STATUS.has(statusCode);
}

/** Coerce a plugin-supplied code into the stable `<PLUGIN>_<CONDITION>` shape. */
export function normalizeProductCode(code: string): string {
  const upper = code
    .toUpperCase()
    .replace(/[^A-Z0-9]+/g, '_')
    .replace(/^_+|_+$/g, '');
  const candidate = /^[A-Z]/.test(upper) ? upper : `PLUGIN_${upper}`;
  return candidate.includes('_') ? candidate : `PLUGIN_${candidate}`;
}

/** Convert scalar plugin details to strings and reject nested report values. */
export function stringifyDetails(details: Record<string, unknown> | undefined): Record<string, string> {
  const result: Record<string, string> = {};
  if (details !== undefined && (typeof details !== 'object' || details === null || Array.isArray(details))) {
    throw createErrorDetailsContractViolation('details', 'must be a flat map of string identifiers');
  }
  for (const [key, value] of Object.entries(details ?? {})) {
    if (value === undefined) {
      continue;
    }
    if (typeof value === 'string') {
      if (isSerializedStructuredValue(value)) {
        throw createErrorDetailsContractViolation(key, 'contains serialized JSON');
      }
      result[key] = value;
    } else if (typeof value === 'number' || typeof value === 'boolean') {
      result[key] = String(value);
    } else {
      throw createErrorDetailsContractViolation(key, 'must be a string identifier');
    }
  }
  return result;
}

export interface ProductErrorInput extends ToEnvelopeOptions {
  code: string;
  message: string;
  retryable: boolean;
  details?: Record<string, string>;
  /** Defaults to a generic hint; product plugins should supply their own. */
  hint?: string;
}

/** Envelope for a product-plugin code that is not in the platform catalog. */
export function createProductErrorEnvelope(input: ProductErrorInput): ErrorEnvelope {
  if (input.details !== undefined && (typeof input.details !== 'object' || input.details === null || Array.isArray(input.details))) {
    throw createErrorDetailsContractViolation('details', 'must be a flat map of string identifiers');
  }
  for (const [key, value] of Object.entries(input.details ?? {})) {
    if (typeof value !== 'string') {
      throw createErrorDetailsContractViolation(key, 'must be a string identifier');
    }
    if (isSerializedStructuredValue(value)) {
      throw createErrorDetailsContractViolation(key, 'contains serialized JSON');
    }
  }
  const envelope: ErrorEnvelope = {
    code: normalizeProductCode(input.code),
    area: 'product',
    stage: 'run',
    severity: 'error',
    retryable: input.retryable,
    message: input.message,
    hint: input.hint ?? PRODUCT_FALLBACK_HINT,
  };
  if (input.details && Object.keys(input.details).length > 0) {
    envelope.details = input.details;
  }
  if (input.correlationId) {
    envelope.correlationId = input.correlationId;
  }
  if (input.actions && input.actions.length > 0) {
    envelope.actions = input.actions;
  }
  if (input.docs) {
    envelope.docs = input.docs;
  }
  return envelope;
}
