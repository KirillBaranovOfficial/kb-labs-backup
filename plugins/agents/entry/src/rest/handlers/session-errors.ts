import { PluginError } from '@kb-labs/sdk';

export function agentSessionNotFound(sessionId: string): PluginError {
  return new PluginError(
    'KB_RUNTIME_RESOURCE_NOT_FOUND',
    `Agent session ${sessionId} was not found.`,
    404,
    { plugin: '@kb-labs/agents', sessionId },
  );
}
