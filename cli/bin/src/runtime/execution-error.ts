import {
  ServicePortConfigError,
  mapErrorEnvelopeToExitCode,
  toErrorEnvelope,
} from '@kb-labs/core-platform';
import { platform } from '@kb-labs/core-runtime';
import {
  CliError,
  mapCliErrorToExitCode,
  type Presenter,
} from '@kb-labs/cli-runtime';

export function handleExecutionError(error: unknown, presenter: Presenter): number {
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
    const envelope = toErrorEnvelope(error);
    if (presenter.isJSON && typeof presenter.json === 'function') {
      presenter.json({ ok: false, error: envelope });
    } else if (typeof presenter.error === 'function') {
      presenter.error(`${envelope.message} [${envelope.code}]\nHint: ${envelope.hint}`);
    }
    return mapErrorEnvelopeToExitCode(envelope.code);
  }

  const envelope = toErrorEnvelope(error);
  if (envelope.code === 'KB_RUNTIME_UNEXPECTED') {
    const cause = error instanceof Error ? error : new Error(String(error));
    platform.logger.error('CLI command failed unexpectedly', cause, {
      correlationId: envelope.correlationId,
    });
  }
  if (presenter.isJSON && typeof presenter.json === 'function') {
    presenter.json({ ok: false, error: envelope });
  } else if (typeof presenter.error === 'function') {
    presenter.error(`${envelope.message} [${envelope.code}]${envelope.hint ? `\nHint: ${envelope.hint}` : ''}`);
  }
  return mapErrorEnvelopeToExitCode(envelope.code);
}
