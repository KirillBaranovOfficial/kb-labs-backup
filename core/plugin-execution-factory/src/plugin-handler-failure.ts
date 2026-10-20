import {
  createContextLogger,
  createErrorEnvelope,
  describeContractValue,
  ErrorEnvelopeError,
  toErrorEnvelope,
  validateErrorEnvelope,
  hasErrorEnvelopeIdentity,
  isErrorCode,
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
  evidenceType?: string,
  evidencePreview?: string,
  contractDiagnostic?: { sourceCode: unknown; sourceHintType: string },
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
    message: contractDiagnostic ? 'Plugin handler returned a malformed failure envelope.' : envelope.message,
    fields: {
      code: envelope.code,
      pluginId: context.pluginId,
      pluginVersion: context.pluginVersion,
      command: context.command,
      failureReason: reason,
      correlationId: context.correlationId,
      ...(!contractDiagnostic ? { serializedError: logger.redactSensitiveValues(context.serializedError ?? error) } : {}),
      ...(contractDiagnostic ? {
        sourceCode: typeof contractDiagnostic.sourceCode === 'string'
          ? logger.redactSensitiveValues(contractDiagnostic.sourceCode)
          : contractDiagnostic.sourceCode === undefined
            ? 'undefined'
            : typeof contractDiagnostic.sourceCode,
        sourceHintType: contractDiagnostic.sourceHintType,
      } : {}),
      ...(malformedField ? {
        malformedField,
        malformedFieldType: evidenceType,
        ...(!contractDiagnostic ? { malformedFieldPreview: evidencePreview } : {}),
      } : {}),
    },
    ...(!contractDiagnostic ? { error: logError } : {}),
  });
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
  const sourceCode = typeof envelopeCandidate === 'object' && envelopeCandidate !== null && 'code' in envelopeCandidate
    ? envelopeCandidate.code
    : undefined;
  const candidateIssues = candidateHasEnvelopeFields ? validateErrorEnvelope(envelopeCandidate) : [];
  const cataloguedHintRepair = candidateIssues.length === 1 && candidateIssues[0]?.field === 'hint' &&
    invalidHintCanUseCatalog(envelopeCandidate, sourceCode)
    ? toErrorEnvelope(withoutHint(envelopeCandidate), { correlationId: context.correlationId })
    : undefined;
  const validationIssues = cataloguedHintRepair
    ? validateErrorEnvelope(cataloguedHintRepair)
    : candidateIssues;
  const invalidEnvelopeIssue = validationIssues[0];
  const typedEnvelope = returnedCandidate !== undefined
    ? (candidateHasEnvelopeFields && validationIssues.length === 0) || thinCommandError
    : error instanceof ErrorEnvelopeError ||
      (candidateHasEnvelopeFields && validationIssues.length === 0) || thinCommandError;
  const canonicalEnvelope = !invalidEnvelopeIssue && typedEnvelope
    ? cataloguedHintRepair ?? toErrorEnvelope(error instanceof ErrorEnvelopeError ? error : envelopeCandidate, { correlationId: context.correlationId })
    : undefined;
  const contractEnvelope = canonicalEnvelope?.code === 'KB_PLUGIN_CONTRACT_VIOLATION' ||
    canonicalEnvelope?.code === 'KB_RUNTIME_DIAGNOSTIC_CONTRACT_VIOLATION'
    ? canonicalEnvelope
    : undefined;
  const contractField = typeof contractEnvelope?.details?.field === 'string'
    ? contractEnvelope.details.field
    : undefined;
  const malformedField = invalidEnvelopeIssue?.field ??
    contractField ??
    (context.returnedFailure && !typedEnvelope ? 'envelope' : undefined);
  const malformedValue = malformedField === 'envelope'
    ? envelopeCandidate
    : typeof envelopeCandidate === 'object' && envelopeCandidate !== null
      ? Reflect.get(envelopeCandidate, malformedField ?? '')
      : undefined;
  const contextLogger = createContextLogger(context.logger, context.logContext);
  const observedEvidence = contractEnvelope?.details?.observedType &&
    contractEnvelope.details.observedValuePreview
    ? {
        observedType: contractEnvelope.details.observedType,
        observedValuePreview: contractEnvelope.details.observedValuePreview,
      }
    : describeContractValue(malformedValue);
  const safeObservedEvidence = {
    observedType: observedEvidence.observedType,
    observedValuePreview: contextLogger.redactSensitiveValues(observedEvidence.observedValuePreview).slice(0, 256),
  };
  const malformedReturn = Boolean(invalidEnvelopeIssue) ||
    Boolean(context.returnedFailure && !typedEnvelope) ||
    canonicalEnvelope?.code === 'KB_PLUGIN_CONTRACT_VIOLATION';
  const rawEnvelope = malformedReturn
    ? createErrorEnvelope('KB_PLUGIN_CONTRACT_VIOLATION', {
        correlationId: context.correlationId,
        message: `Plugin ${context.pluginId} command ${context.command} returned malformed error field "${malformedField}".`,
        hint: 'Return a valid product or platform error envelope.',
        details: { plugin: context.pluginId, command: context.command, field: malformedField ?? 'envelope', ...safeObservedEvidence },
      })
    : canonicalEnvelope
      ? canonicalEnvelope
      : createErrorEnvelope('KB_PLUGIN_COMMAND_FAILED', {
          correlationId: context.correlationId,
          details: {
            plugin: context.pluginId,
            command: context.command,
            failureReason: reason,
          },
        });
  const envelope = contextLogger.redactSensitiveValues(rawEnvelope);
  const contractDiagnostic = envelope.code === 'KB_PLUGIN_CONTRACT_VIOLATION'
    ? { sourceCode, sourceHintType: sourceHintType(envelopeCandidate) }
    : undefined;
  if (typedEnvelope) {
    recordPluginFailure(context, envelope, context.serializedError ?? serializeError(error), 'typed product or platform error', context.diagnosticStack, malformedField, safeObservedEvidence.observedType, safeObservedEvidence.observedValuePreview, contractDiagnostic);
    return envelope;
  }
  recordPluginFailure(context, envelope, context.serializedError ?? serializeError(error), reason, context.diagnosticStack, malformedField, safeObservedEvidence.observedType, safeObservedEvidence.observedValuePreview, contractDiagnostic);
  return envelope;
}

function sourceHintType(value: unknown): string {
  if (typeof value !== 'object' || value === null || !Object.hasOwn(value, 'hint')) return 'absent';
  const hint = Reflect.get(value, 'hint');
  if (hint === undefined) return 'undefined';
  if (hint === null) return 'null';
  return typeof hint;
}

function invalidHintCanUseCatalog(value: unknown, code: unknown): value is Record<string, unknown> {
  return hasErrorEnvelopeIdentity(value) && typeof code === 'string' && isErrorCode(code) &&
    Object.hasOwn(value, 'hint') && typeof Reflect.get(value, 'hint') !== 'string';
}

function withoutHint(value: Record<string, unknown>): Record<string, unknown> {
  const canonical = { ...value };
  delete canonical.hint;
  return canonical;
}
