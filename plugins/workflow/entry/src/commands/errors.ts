import type { CommandFailure } from '@kb-labs/sdk';
import type { PlatformErrorCode } from '@kb-labs/core-platform';

type WorkflowErrorCode = Extract<PlatformErrorCode,
  'KB_RUNTIME_INPUT_INVALID' | 'KB_RUNTIME_COMMAND_FAILED' | 'KB_RUNTIME_CRON_PLUGIN_READ_ONLY'>;

export function workflowCommandError(
  code: WorkflowErrorCode,
  message: string,
  hint: string,
  cause?: string,
): CommandFailure {
  return { ok: false, error: { code, message, hint, ...(cause ? { details: { cause } } : {}) } };
}
