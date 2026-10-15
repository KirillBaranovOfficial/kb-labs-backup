import {
  ServicePortConfigError,
  mapErrorEnvelopeToExitCode,
  createContextLogger,
  createErrorEnvelope,
  createProcessRootLogContext,
  toErrorEnvelope,
  type ErrorEnvelope,
} from '@kb-labs/core-platform';
import { platform } from '@kb-labs/core-runtime';
import {
  CliError,
  mapCliErrorToExitCode,
  type Presenter,
} from '@kb-labs/cli-runtime';

export function handleExecutionError(
  error: unknown,
  presenter: Presenter,
  options: { correlationId?: string; envelope?: ErrorEnvelope } = {},
): number {
  if (error instanceof CliError) {
    const exitCode = mapCliErrorToExitCode(error.code);
    if (presenter.isJSON && typeof presenter.json === 'function') {
      presenter.json({
        ok: false,
        error: {
          code: error.code,
          message: error.message,
          ...(error.details != null && { details: error.details }),
        },
      });
    } else if (typeof presenter.error === 'function') {
      presenter.error(`${error.code}: ${error.message}`);
    }
    return exitCode;
  }

  if (error instanceof ServicePortConfigError) {
    const envelope = toErrorEnvelope(error, { correlationId: options.correlationId });
    if (presenter.isJSON && typeof presenter.json === 'function') {
      presenter.json({ ok: false, error: envelope });
    } else if (typeof presenter.error === 'function') {
      presenter.error(`${envelope.message} [${envelope.code}]\nHint: ${envelope.hint}`);
    }
    return mapErrorEnvelopeToExitCode(envelope.code);
  }

  let envelope = options.envelope ?? toErrorEnvelope(error, { correlationId: options.correlationId });
  if (options.correlationId && !envelope.correlationId) {
    envelope = { ...envelope, correlationId: options.correlationId };
  }
  if (envelope.code === 'KB_RUNTIME_UNEXPECTED') {
    envelope = createErrorEnvelope('KB_RUNTIME_UNEXPECTED', {
      correlationId: envelope.correlationId,
    });
    const cause = error instanceof Error ? error : new Error(String(error));
    const logger = createContextLogger(
      platform.logger,
      createProcessRootLogContext({
        applicationId: 'kb',
        serviceId: 'kb',
        instanceId: `${process.pid}`,
        layer: 'cli',
      }),
    ).forComponent('cli-error-boundary').forOperation('cli.command.failed', {
      correlationId: envelope.correlationId,
    });
    logger.event('error', {
      event: 'cli.unexpected_error',
      message: 'CLI command failed unexpectedly',
      error: cause,
      fields: {
        errorClass: cause.name,
        errorMessage: cause.message,
        causeChain: errorCauseChain(cause),
        stack: cause.stack ?? '',
      },
    });
  }
  if (presenter.isJSON && typeof presenter.json === 'function') {
    presenter.json({ ok: false, error: envelope });
  } else if (typeof presenter.error === 'function') {
    presenter.error(`${envelope.message} [${envelope.code}]${envelope.hint ? `\nHint: ${envelope.hint}` : ''}`);
  }
  return mapErrorEnvelopeToExitCode(envelope.code);
}


function errorCauseChain(error: Error): Array<{ name: string; message: string; stack?: string }> {
  const chain: Array<{ name: string; message: string; stack?: string }> = [];
  const seen = new Set<Error>();
  let current: unknown = error;
  while (current instanceof Error && !seen.has(current)) {
    seen.add(current);
    chain.push({
      name: current.name,
      message: current.message,
      ...(current.stack ? { stack: current.stack } : {}),
    });
    current = current.cause;
  }
  if (current !== undefined && !(current instanceof Error)) {
    chain.push({ name: 'Cause', message: String(current) });
  }
  return chain;
}
