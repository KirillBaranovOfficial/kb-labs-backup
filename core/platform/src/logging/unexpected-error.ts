import type { ILogger } from '../adapters/logger.js';

/** Write the queryable diagnostic record for an unexpected CLI failure. */
export function logUnexpectedCliError(
  logger: ILogger,
  error: unknown,
  correlationId?: string,
): void {
  const cause = error instanceof Error ? error : new Error(String(error));
  logger.error('CLI command failed unexpectedly', cause, {
    event: 'cli.unexpected_error',
    ...(correlationId ? { correlationId } : {}),
    errorClass: cause.name,
    errorMessage: cause.message,
    causeChain: errorCauseChain(cause),
    stack: cause.stack ?? '',
  });
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
