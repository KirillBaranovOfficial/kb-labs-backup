/**
 * @module @kb-labs/core-platform/error-envelope
 *
 * Canonical execution response contracts.
 */

import type { ExecutionTarget } from './execution-request.js';
import { ErrorEnvelopeError, type ErrorEnvelope } from './types.js';

/** JSON-safe exception detail used only beside public error envelopes. */
export interface SerializedError {
  name: string;
  message: string;
  stack?: string;
  cause?: SerializedError;
}

/** Reconstructed IPC failure that preserves both its public envelope and source diagnostics. */
export class SerializedErrorEnvelopeError extends ErrorEnvelopeError {
  constructor(
    envelope: ErrorEnvelope,
    readonly serializedError: SerializedError,
  ) {
    super(envelope);
    this.name = serializedError.name;
    this.message = serializedError.message;
    this.stack = serializedError.stack;
  }
}

/** The sole producer for thrown and transported handler diagnostics. */
export function serializeError(value: unknown): SerializedError {
  if (value instanceof SerializedErrorEnvelopeError) return value.serializedError;
  if (value instanceof Error) {
    const cause = value.cause === undefined ? undefined : serializeError(value.cause);
    return {
      name: value.name || 'Error',
      message: value.message,
      ...(value.stack ? { stack: value.stack } : {}),
      ...(cause ? { cause } : {}),
    };
  }

  if (isSerializedError(value)) return value;

  return {
    name: 'NonErrorThrown',
    message: typeof value === 'string' ? value : String(value),
  };
}

function isSerializedError(value: unknown): value is SerializedError {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return false;
  const candidate = value as Record<string, unknown>;
  return typeof candidate.name === 'string' &&
    typeof candidate.message === 'string' &&
    (candidate.stack === undefined || typeof candidate.stack === 'string');
}

export interface ExecutionMeta {
  startTime: number;
  endTime: number;
  duration: number;
  pluginId: string;
  pluginVersion: string;
  handlerId?: string;
  requestId: string;
  tenantId?: string;
}

export interface RunResult<T = unknown> {
  ok: boolean;
  data?: T;
  error?: SerializedError;
  executionMeta: ExecutionMeta;
}

/**
 * Standardized execution-layer error codes.
 */
export type ExecutionErrorCode =
  | 'TIMEOUT'
  | 'ABORTED'
  | 'PERMISSION_DENIED'
  | 'HANDLER_CONTRACT_ERROR'
  | 'HANDLER_NOT_FOUND'
  | 'WORKSPACE_ERROR'
  | 'VALIDATION_ERROR'
  | 'UNKNOWN_ERROR'
  | 'QUEUE_FULL'
  | 'ACQUIRE_TIMEOUT'
  | 'WORKER_CRASHED'
  | 'WORKER_UNHEALTHY'
  | 'NO_HOST_AVAILABLE';

export type ExecutionError = ErrorEnvelope & { error: SerializedError };

/** Private execution diagnostics travel beside the user-facing failure envelope. */
export interface ExecutionDiagnostics {
  errorStack?: string;
}

export interface ExecutionMetadata {
  workerId?: string;
  workspaceId?: string;
  memoryUsedMB?: number;
  handlerWasWarmed?: boolean;
  backend?: 'in-process' | 'subprocess' | 'worker-pool' | 'remote';
  executionMeta?: ExecutionMeta;
  target?: ExecutionTarget;
  [key: string]: unknown;
}

/**
 * Canonical execution result.
 */
export interface ExecutionResult {
  ok: boolean;
  data?: unknown;
  error?: ExecutionError;
  diagnostics?: ExecutionDiagnostics;
  executionTimeMs: number;
  artifactIds?: string[];
  metadata?: ExecutionMetadata;
  [key: string]: unknown;
}

/**
 * Canonical response alias.
 */
export type ExecutionResponse = ExecutionResult;

export interface ExecuteOptions {
  signal?: AbortSignal;
  pluginInvoker?: <T = unknown>(
    pluginId: string,
    input?: unknown,
    options?: unknown
  ) => Promise<T>;
  [key: string]: unknown;
}
