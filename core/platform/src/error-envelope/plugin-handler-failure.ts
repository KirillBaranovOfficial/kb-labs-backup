import type { ILogger } from '../adapters/logger.js';
import { createContextLogger, type PlatformLogContext } from '../logging/context.js';
import { createErrorEnvelope } from './catalog.js';
import { toErrorEnvelope } from './from-error.js';
import type { ErrorEnvelope } from './types.js';

export interface PluginHandlerFailureContext {
  pluginId: string;
  command: string;
  correlationId: string;
  logger: ILogger;
  logContext: PlatformLogContext;
  sensitiveValues: readonly string[];
}

type FailureReason = 'failed unexpectedly' | 'timed out' | 'execution process crashed';

function failureReason(error: unknown): FailureReason {
  if (typeof error === 'object' && error !== null && 'details' in error &&
      typeof error.details === 'object' && error.details !== null && 'failureReason' in error.details) {
    const reason = error.details.failureReason;
    if (reason === 'timed out' || reason === 'execution process crashed') return reason;
  }
  if (error instanceof Error && error.name === 'TimeoutError') return 'timed out';
  if (typeof error === 'object' && error !== null && 'code' in error && error.code === 'TIMEOUT') {
    return 'timed out';
  }
  if (error instanceof Error && error.name === 'WorkerCrashedError') return 'execution process crashed';
  if (typeof error === 'object' && error !== null && 'code' in error && error.code === 'WORKER_CRASHED') {
    return 'execution process crashed';
  }
  return 'failed unexpectedly';
}

function asError(error: unknown): Error {
  if (error instanceof Error) return error;
  return new Error('Plugin handler threw a non-error value.');
}

function malformedProductField(error: unknown): string {
  const record = typeof error === 'object' && error !== null ? error as Record<string, unknown> : {};
  for (const field of ['message', 'hint', 'retryable'] as const) {
    if (field === 'retryable' ? typeof record[field] !== 'boolean' : typeof record[field] !== 'string' || !record[field]) {
      return field;
    }
  }
  if (record.details !== undefined && (typeof record.details !== 'object' || record.details === null || Array.isArray(record.details))) {
    return 'details';
  }
  return 'error envelope';
}

/** The platform-owned normaliser for every plugin handler failure surface. */
export function normalizePluginHandlerFailure(
  error: unknown,
  context: PluginHandlerFailureContext,
): ErrorEnvelope {
  const reason = failureReason(error);
  const normalized = reason === 'failed unexpectedly'
    ? toErrorEnvelope(error, { correlationId: context.correlationId })
    : createErrorEnvelope('KB_RUNTIME_UNEXPECTED', {
        correlationId: context.correlationId,
        details: { failureReason: reason },
      });
  const malformedFailure = normalized.code === 'KB_PLUGIN_CONTRACT_VIOLATION';

  if (malformedFailure && normalized.details?.plugin === context.pluginId &&
      normalized.details.command === context.command && normalized.correlationId === context.correlationId) {
    return normalized;
  }

  if (!malformedFailure && normalized.code !== 'KB_RUNTIME_UNEXPECTED' &&
      reason === 'failed unexpectedly') {
    return normalized;
  }

  const field = malformedProductField(error);
  const failure = malformedFailure
    ? createErrorEnvelope('KB_PLUGIN_CONTRACT_VIOLATION', {
        correlationId: context.correlationId,
        message: `Plugin ${context.pluginId} returned malformed error field "${field}".`,
        hint: 'Return a product error with a valid code, message, hint, retryable value, and flat details.',
        details: { plugin: context.pluginId, command: context.command, field },
      })
    : createErrorEnvelope('KB_PLUGIN_COMMAND_FAILED', {
        correlationId: context.correlationId,
        details: {
          plugin: context.pluginId,
          command: context.command,
          correlationId: context.correlationId,
          failureReason: reason,
        },
      });
  const logger = createContextLogger(context.logger, context.logContext)
    .forOperation('plugin.handler.failed', { correlationId: context.correlationId })
    .forPlugin({ pluginId: context.pluginId })
    .withSensitiveValues(context.sensitiveValues);
  const logError = asError(error);
  if (typeof error === 'object' && error !== null && 'stack' in error && typeof error.stack === 'string') {
    logError.stack = error.stack;
  }
  logger.event('error', {
    event: 'plugin.handler.failed',
    message: failure.message,
    fields: {
      code: failure.code,
      command: context.command,
      failureReason: reason,
      correlationId: context.correlationId,
    },
    error: logError,
  });
  return failure;
}
