import type { CommandFailure } from '@kb-labs/sdk';

type WorkflowErrorCode = 'KB_RUNTIME_INPUT_INVALID' | 'KB_RUNTIME_COMMAND_FAILED';

export function workflowCommandError(
  _ctx: unknown,
  code: WorkflowErrorCode,
  message: string,
  hint: string,
  _outputJson: boolean,
  cause?: string,
): CommandFailure {
  return { ok: false, error: { code, message, hint, ...(cause ? { details: { cause } } : {}) } };
}
