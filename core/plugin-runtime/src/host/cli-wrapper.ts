/**
 * CLI Host Wrapper
 *
 * Transforms RunResult<T> from runner layer into CLI-specific CommandResultWithMeta<T>.
 */

import type {
  RunResult,
  ExecutionMeta,
  CommandFailure,
  CommandResult,
  CommandResultWithMeta,
  StandardMeta,
  PluginContextDescriptor,
} from '@kb-labs/plugin-contracts';

/**
 * A command failure is the only structured handler result. Success values are
 * always plain command data, even when that data contains an `ok` field.
 */
function isCommandFailure(value: unknown): value is CommandFailure {
  return (
    typeof value === 'object' &&
    value !== null &&
    'ok' in value &&
    value.ok === false &&
    'error' in value &&
    typeof value.error === 'object' && value.error !== null
  );
}

export function isHandlerSuccessEnvelope(value: unknown): boolean {
  return typeof value === 'object' && value !== null &&
    'ok' in value && value.ok === true && 'data' in value;
}

/**
 * Convert ExecutionMeta to StandardMeta format
 */
function toStandardMeta(
  executionMeta: ExecutionMeta,
  descriptor: PluginContextDescriptor
): StandardMeta {
  return {
    executedAt: new Date(executionMeta.startTime).toISOString(),
    duration: executionMeta.duration,
    pluginId: executionMeta.pluginId,
    pluginVersion: executionMeta.pluginVersion,
    commandId: executionMeta.handlerId,
    host: descriptor.hostType,
    tenantId: executionMeta.tenantId,
    requestId: executionMeta.requestId,
  };
}

/**
 * Wrap RunResult from runner into CLI-specific CommandResultWithMeta
 *
 * CLI commands can return either:
 * - CommandFailure for an operation failure
 * - T directly (data only, defaults to exitCode: 0)
 * - void/undefined (defaults to exitCode: 0)
 *
 * @param runResult - Result from runInProcess/runInSubprocess
 * @param descriptor - Plugin context descriptor for additional metadata
 * @returns CommandResultWithMeta<T> for CLI consumption
 */
export function wrapCliResult<T>(
  runResult: RunResult<CommandResult<T> | T | void>,
  descriptor: PluginContextDescriptor
): CommandResultWithMeta<T> {
  const { data, executionMeta } = runResult;
  const standardMeta = toStandardMeta(executionMeta, descriptor);

  // A handler must return its payload; only this boundary builds the success envelope.
  if (isHandlerSuccessEnvelope(data)) {
    return {
      exitCode: 1,
      ok: false,
      error: {
        code: 'KB_PLUGIN_CONTRACT_VIOLATION',
        message: 'Command handlers must return plain command data.',
        hint: 'Return the payload without the { ok: true, data: ... } envelope, then retry the command.',
      },
      meta: standardMeta as StandardMeta & Record<string, unknown>,
    };
  }

  // Case 1: Handler returned the shared typed failure.
  if (isCommandFailure(data)) {
    const mergedMeta: StandardMeta & Record<string, unknown> = {
      ...standardMeta,
    };

    return {
      exitCode: 1,
      ok: false,
      error: data.error,
      meta: mergedMeta,
    };
  }

  // Case 2: Handler returned void/undefined
  if (data === undefined || data === null) {
    return {
      exitCode: 0,
      ok: true,
      meta: standardMeta as StandardMeta & Record<string, unknown>,
    };
  }

  // Case 3: Handler returned raw data T
  return {
    exitCode: 0,
    ok: true,
    result: data as T,
    meta: standardMeta as StandardMeta & Record<string, unknown>,
  };
}
