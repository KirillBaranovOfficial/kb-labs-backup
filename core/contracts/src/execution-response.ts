/**
 * @module @kb-labs/core-contracts
 *
 * Canonical execution response contracts.
 */

import type { ExecutionTarget } from './execution-request.js';

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
  error?: {
    name: string;
    message: string;
    code?: string;
    stack?: string;
  };
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

export interface ExecutionError {
  message: string;
  /** Handler errors can carry the platform or product code used by the shared UX contract. */
  code?: string;
  stack?: string;
  details?: Record<string, unknown>;
  [key: string]: unknown;
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
  executionTimeMs: number;
  artifactIds?: string[];
  metadata?: ExecutionMetadata;
  [key: string]: unknown;
}

/**
 * Canonical response alias.
 */
export type ExecutionResponse = ExecutionResult;

/** Failure value produced by the execution factory after its sole normalization boundary. */
export interface NormalizedExecutionFailure<TEnvelope = unknown> extends ExecutionError {
  envelope: TEnvelope;
  normalized: true;
}

/** Result visible to runtime surfaces; failed results can only carry the shared envelope type. */
export type NormalizedExecutionResult<TEnvelope = unknown> =
  | {
      ok: true;
      data?: unknown;
      executionTimeMs: number;
      artifactIds?: string[];
      metadata?: ExecutionMetadata;
      [key: string]: unknown;
    }
  | {
      ok: false;
      error: NormalizedExecutionFailure<TEnvelope>;
      executionTimeMs: number;
      metadata?: ExecutionMetadata;
      [key: string]: unknown;
    };

export interface ExecuteOptions {
  signal?: AbortSignal;
  pluginInvoker?: <T = unknown>(
    pluginId: string,
    input?: unknown,
    options?: unknown
  ) => Promise<T>;
  [key: string]: unknown;
}
