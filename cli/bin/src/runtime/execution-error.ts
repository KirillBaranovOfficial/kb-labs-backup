import { cliBinaryName } from './cli-binary.js';
import {
  ServicePortConfigError,
  mapErrorEnvelopeToExitCode,
  createContextLogger,
  createErrorEnvelope,
  createProcessRootLogContext,
  logUnexpectedCliError,
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
    const envelope = toErrorEnvelope(error, { correlationId: options.correlationId, binary: cliBinaryName });
    if (presenter.isJSON && typeof presenter.json === 'function') {
      presenter.json({ ok: false, error: envelope });
    } else if (typeof presenter.error === 'function') {
      presenter.error(`${envelope.message} [${envelope.code}]\nHint: ${envelope.hint}`);
    }
    return mapErrorEnvelopeToExitCode(envelope.code);
  }

  let envelope = options.envelope ?? toErrorEnvelope(error, { correlationId: options.correlationId, binary: cliBinaryName });
  if (options.correlationId && !envelope.correlationId) {
    envelope = { ...envelope, correlationId: options.correlationId };
  }
  if (envelope.code === 'KB_RUNTIME_UNEXPECTED') {
    envelope = createErrorEnvelope('KB_RUNTIME_UNEXPECTED', {
      correlationId: envelope.correlationId,
    });
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
    logUnexpectedCliError(logger, error, envelope.correlationId);
  }
  if (presenter.isJSON && typeof presenter.json === 'function') {
    presenter.json({ ok: false, error: envelope });
  } else if (typeof presenter.error === 'function') {
    presenter.error(`${envelope.message} [${envelope.code}]${envelope.hint ? `\nHint: ${envelope.hint}` : ''}`);
  }
  return mapErrorEnvelopeToExitCode(envelope.code);
}
