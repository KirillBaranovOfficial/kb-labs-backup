import {
  ServicePortConfigError,
  toErrorEnvelope,
} from '@kb-labs/core-platform';
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
    return 1;
  }

  const message = error instanceof Error ? error.message : String(error);
  if (presenter.isJSON && typeof presenter.json === 'function') {
    presenter.json({ ok: false, error: { message } });
  } else if (typeof presenter.error === 'function') {
    presenter.error(message);
  }
  return 1;
}
