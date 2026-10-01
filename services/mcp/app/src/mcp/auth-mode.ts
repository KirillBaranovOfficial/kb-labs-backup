export type McpAuthMode = "local" | "secured";
import { createErrorEnvelope } from '@kb-labs/core-platform';

export function rejectsUnauthenticatedRequest(
  mode: McpAuthMode,
  authenticated: boolean,
): boolean {
  return mode === "secured" && !authenticated;
}

export function mcpUnauthenticatedResponse(): { statusCode: 401; body: { error: ReturnType<typeof createErrorEnvelope> } } {
  return { statusCode: 401, body: { error: createErrorEnvelope('KB_AUTH_REQUIRED') } };
}
