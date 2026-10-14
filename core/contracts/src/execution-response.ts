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
  area?: string;
  stage?: string;
  severity?: string;
  retryable?: boolean;
  cause?: string;
  hint?: string;
  correlationId?: string;
  details?: Record<string, unknown>;
  /** Preserves the nested failure claim so the factory can reject malformed results. */
  error?: unknown;
  actions?: Array<{ id: string; label: string; command?: string }>;
  docs?: string;
  diagnostics?: Array<{
    code: string;
    message: string;
    severity: string;
    path?: string;
    line?: number;
    hint?: string;
  }>;
}

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
