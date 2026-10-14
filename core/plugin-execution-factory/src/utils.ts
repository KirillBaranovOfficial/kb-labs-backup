/**
 * @module @kb-labs/plugin-execution/utils
 *
 * Utility functions for execution layer.
 */

import { randomBytes } from 'node:crypto';
import { createErrorEnvelope, ErrorEnvelopeError, toErrorEnvelope } from '@kb-labs/core-platform';
import type { ExecutionDiagnostics, ExecutionError } from './types.js';
import { TimeoutError, AbortError, isExecutionLayerError } from './errors.js';

/**
 * Create unique execution ID.
 * Format: exec_{pid}_{timestamp}_{random}
 *
 * Includes pid for easier tracing on same machine.
 *
 * @example "exec_12345_1703088000000_a1b2c3d4"
 */
export function createExecutionId(): string {
  const pid = process.pid;
  const timestamp = Date.now();
  const random = randomBytes(4).toString('hex');
  return `exec_${pid}_${timestamp}_${random}`;
}

/**
 * Create promise that rejects after timeout.
 *
 * IMPORTANT:
 * - Uses TimeoutError/AbortError from errors.ts (not generic Error)
 * - Cleans up event listener on abort signal to prevent memory leaks
 */
export function createTimeoutPromise(
  timeoutMs: number,
  signal?: AbortSignal
): Promise<never> {
  return new Promise((_, reject) => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    let abortHandler: (() => void) | undefined;

    // Cleanup function
    const cleanup = () => {
      if (timer) {
        clearTimeout(timer);
        timer = undefined;
      }
      if (signal && abortHandler) {
        signal.removeEventListener('abort', abortHandler);
        abortHandler = undefined;
      }
    };

    // Timer handler
    timer = setTimeout(() => {
      cleanup();
      reject(new TimeoutError(`Timeout after ${timeoutMs}ms`, timeoutMs));
    }, timeoutMs);

    // Abort handler (if signal provided)
    if (signal) {
      // Check if already aborted
      if (signal.aborted) {
        cleanup();
        reject(new AbortError('Execution aborted'));
        return;
      }

      abortHandler = () => {
        cleanup();
        reject(new AbortError('Execution aborted'));
      };
      signal.addEventListener('abort', abortHandler);
    }
  });
}

/**
 * Normalize any error to ExecutionError interface.
 *
 * - Execution-layer control errors become safe catalogued failures.
 * - Typed platform and product errors retain their complete envelope.
 * - Unhandled values become a safe platform unexpected error.
 *
 * Returns strictly typed ExecutionError (interface).
 */
export function normalizeError(error: unknown): ExecutionError {
  if (error instanceof ErrorEnvelopeError) {
    return toErrorEnvelope(error);
  }
  if (isExecutionLayerError(error)) {
    if (error instanceof AbortError) {
      return createErrorEnvelope('KB_PLUGIN_ABORTED', { cause: error.message });
    }
    const details = error instanceof TimeoutError
      ? { failureReason: 'timed out' }
      : error.code === 'WORKER_CRASHED'
        ? { failureReason: 'execution process crashed' }
        : undefined;
    return createErrorEnvelope('KB_RUNTIME_UNEXPECTED', details ? { details } : {});
  }
  if (error instanceof Error) {
    return toErrorEnvelope(error);
  }
  if (typeof error === 'object' && error !== null) {
    const envelope = toErrorEnvelope(error);
    return { ...envelope };
  }

  // Non-error: convert to string
  return {
    message: 'The operation did not complete because the handler threw a non-error value.',
    code: 'KB_RUNTIME_UNEXPECTED',
  };
}

/** Keeps private stack diagnostics beside, never inside, the serialized failure envelope. */
export function executionDiagnostics(error: unknown): ExecutionDiagnostics | undefined {
  return error instanceof Error && error.stack ? { errorStack: error.stack } : undefined;
}

export function normalizeExecutionFailure(error: unknown): {
  error: ExecutionError;
  diagnostics?: ExecutionDiagnostics;
} {
  const diagnostics = executionDiagnostics(error);
  return {
    error: normalizeError(error),
    ...(diagnostics ? { diagnostics } : {}),
  };
}

/**
 * Normalize HTTP headers from Fastify format.
 *
 * Fastify headers can be:
 * - string
 * - string[] (multiple values)
 * - undefined
 *
 * This normalizes to Record<string, string> by joining arrays with comma.
 */
export function normalizeHeaders(
  headers: Record<string, string | string[] | undefined>
): Record<string, string> {
  const result: Record<string, string> = {};

  for (const [key, value] of Object.entries(headers)) {
    if (value === undefined) {continue;}

    if (Array.isArray(value)) {
      result[key] = value.join(', ');
    } else {
      result[key] = value;
    }
  }

  return result;
}
