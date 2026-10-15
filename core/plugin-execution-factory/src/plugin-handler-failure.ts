import { inspect } from 'node:util';
import {
  createContextLogger,
  createErrorEnvelope,
  ErrorEnvelopeError,
  toErrorEnvelope,
  validateErrorEnvelope,
  hasErrorEnvelopeIdentity,
  serializeError,
  type SerializedError,
  type ErrorEnvelope,
  type ILogger,
  type PlatformLogContext,
} from '@kb-labs/core-platform';

export interface PluginHandlerFailureContext {
  pluginId: string;
  pluginVersion: string;
  command: string;
  correlationId: string;
  logger: ILogger;
  logContext: PlatformLogContext;
  returnedFailure?: boolean;
  diagnosticStack?: string;
  serializedError?: SerializedError;
  returnedFailureError?: unknown;
  executionEnvelope?: ErrorEnvelope;
}

type FailureReason = 'failed unexpectedly' | 'timed out' | 'execution process crashed';

function recordPluginFailure(
  context: PluginHandlerFailureContext,
  envelope: ErrorEnvelope,
  error: SerializedError,
  reason: string,
  diagnosticStack?: string,
  malformedField?: string,
  malformedValue?: unknown,
): void {
  const logger = createContextLogger(context.logger, context.logContext)
    .forOperation('plugin.handler.failed', { correlationId: context.correlationId })
    .forPlugin({ pluginId: context.pluginId });
  const logError = asError(error);
  const stack = diagnosticStack ?? error.stack;
  if (stack) {
    logError.stack = stack;
  }
  logger.event('error', {
    event: 'plugin.handler.failed',
    message: envelope.message,
    fields: {
      code: envelope.code,
      pluginId: context.pluginId,
      pluginVersion: context.pluginVersion,
      command: context.command,
      failureReason: reason,
      correlationId: context.correlationId,
      serializedError: logger.redactSensitiveValues(context.serializedError ?? error),
      ...(malformedField ? {
        malformedField,
        malformedFieldType: runtimeType(malformedValue),
        malformedFieldPreview: boundedPreview(logger, malformedValue),
      } : {}),
    },
    error: logError,
  });
}

function runtimeType(value: unknown): string {
  if (value === null) return 'null';
  if (Array.isArray(value)) return 'array';
  if (value instanceof Error) return 'error';
  return typeof value;
}

function boundedPreview(logger: ReturnType<typeof createContextLogger>, value: unknown): string {
  const redacted = logger.redactSensitiveValues(value);
  return inspect(redacted, {
    depth: 3,
    maxArrayLength: 8,
    maxStringLength: 128,
    breakLength: Infinity,
  }).slice(0, 256);
}

function failureReason(error: unknown): FailureReason {
  if (typeof error === 'object' && error !== null && 'details' in error &&
      typeof error.details === 'object' && error.details !== null && 'failureReason' in error.details) {
    const reason = error.details.failureReason;
    if (reason === 'timed out' || reason === 'execution process crashed') return reason;
  }
  if (error instanceof Error && error.name === 'TimeoutError') return 'timed out';
  if (typeof error === 'object' && error !== null && 'code' in error && error.code === 'TIMEOUT') return 'timed out';
  if (error instanceof Error && error.name === 'WorkerCrashedError') return 'execution process crashed';
  if (typeof error === 'object' && error !== null && 'code' in error && error.code === 'WORKER_CRASHED') return 'execution process crashed';
  return 'failed unexpectedly';
}

function asError(error: SerializedError): Error {
  const logError = new Error(error.message);
  logError.name = error.name;
  logError.stack = error.stack;
  return logError;
}

/** The platform-owned normaliser for every plugin handler failure surface. */
export function normalizePluginHandlerFailure(
  error: unknown,
  context: PluginHandlerFailureContext,
): ErrorEnvelope {
  const reason = failureReason(error);
  const returnedCandidate = context.returnedFailureError;
  const envelopeCandidate = returnedCandidate !== undefined
    ? returnedCandidate
    : context.executionEnvelope
      ? context.executionEnvelope
    : error instanceof ErrorEnvelopeError
      ? error.envelope
      : typeof error === 'object' && error !== null && 'envelope' in error
        ? error.envelope
        : typeof error === 'object' && error !== null && 'error' in error
          ? error.error
          : error;
  const candidateHasEnvelopeFields = hasErrorEnvelopeIdentity(envelopeCandidate);
  const thinCommandError = typeof envelopeCandidate === 'object' && envelopeCandidate !== null &&
    !Array.isArray(envelopeCandidate) && !(envelopeCandidate instanceof Error) &&
    'code' in envelopeCandidate && typeof envelopeCandidate.code === 'string';
  const validationIssues = candidateHasEnvelopeFields ? validateErrorEnvelope(envelopeCandidate) : [];
  const invalidEnvelopeIssue = validationIssues[0];
  const typedEnvelope = returnedCandidate !== undefined
    ? (candidateHasEnvelopeFields && validationIssues.length === 0) || thinCommandError
    : error instanceof ErrorEnvelopeError ||
      (candidateHasEnvelopeFields && validationIssues.length === 0) || thinCommandError;
  const canonicalEnvelope = !invalidEnvelopeIssue && typedEnvelope
    ? toErrorEnvelope(error instanceof ErrorEnvelopeError ? error : envelopeCandidate, { correlationId: context.correlationId })
    : undefined;
  const malformedField = invalidEnvelopeIssue?.field ??
    (canonicalEnvelope?.code === 'KB_PLUGIN_CONTRACT_VIOLATION' ? canonicalEnvelope.details?.field ?? 'envelope' : undefined) ??
    (context.returnedFailure && !typedEnvelope ? 'envelope' : undefined);
  const malformedValue = malformedField === 'envelope'
    ? envelopeCandidate
    : typeof envelopeCandidate === 'object' && envelopeCandidate !== null
      ? Reflect.get(envelopeCandidate, malformedField ?? '')
      : undefined;
  const rawEnvelope = malformedField
    ? createErrorEnvelope('KB_PLUGIN_CONTRACT_VIOLATION', {
        correlationId: context.correlationId,
        message: `Plugin ${context.pluginId} command ${context.command} returned malformed error field "${malformedField}".`,
        hint: 'Return a valid product or platform error envelope.',
        details: { plugin: context.pluginId, command: context.command, field: malformedField },
      })
    : canonicalEnvelope
      ? canonicalEnvelope
      : createErrorEnvelope('KB_PLUGIN_COMMAND_FAILED', {
          correlationId: context.correlationId,
          details: {
            plugin: context.pluginId,
            command: context.command,
            correlationId: context.correlationId,
            failureReason: reason,
          },
        });
  const envelope = createContextLogger(context.logger, context.logContext)
    .redactSensitiveValues(rawEnvelope);
  if (typedEnvelope) {
    recordPluginFailure(context, envelope, context.serializedError ?? serializeError(error), 'typed product or platform error', context.diagnosticStack, malformedField, malformedValue);
    return envelope;
  }
  recordPluginFailure(context, envelope, context.serializedError ?? serializeError(error), reason, context.diagnosticStack, malformedField, malformedValue);
  return envelope;
}
