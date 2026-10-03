import { createErrorEnvelope } from '@kb-labs/core-platform/error-envelope';
import { handleError, type PluginContextV3 } from '@kb-labs/sdk';

type WorkflowErrorCode = 'KB_RUNTIME_INPUT_INVALID' | 'KB_RUNTIME_COMMAND_FAILED';

export function workflowCommandError(
  ctx: PluginContextV3,
  code: WorkflowErrorCode,
  message: string,
  hint: string,
  outputJson: boolean,
  cause?: string,
): void {
  const envelope = createErrorEnvelope(code, { message, hint, cause });
  handleError(ctx, { envelope }, outputJson);
}
