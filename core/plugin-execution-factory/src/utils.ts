/**
 * @module @kb-labs/plugin-execution/utils
 *
 * Utility functions for execution layer.
 */

import { randomBytes } from 'node:crypto';
import { createErrorEnvelope, ErrorEnvelopeError, toErrorEnvelope } from '@kb-labs/core-platform';
import type { ExecutionError } from './types.js';
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
    const envelope = toErrorEnvelope(error);
    return { ...envelope, stack: error.stack };
  }
  if (isExecutionLayerError(error)) {
    if (error instanceof AbortError) {
      const envelope = createErrorEnvelope('KB_PLUGIN_ABORTED', { cause: error.message });
      return { ...envelope, stack: error.stack };
    }
    const envelope = createErrorEnvelope('KB_RUNTIME_UNEXPECTED');
    return { ...envelope, stack: error.stack };
  }
  if (error instanceof Error) {
    const envelope = toErrorEnvelope(error);
    return { ...envelope, stack: error.stack };
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
