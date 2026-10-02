export type McpAuthMode = "local" | "secured";
import { randomUUID } from 'node:crypto';
import { createErrorEnvelope } from '@kb-labs/core-platform';

export function isMcpCorsPreflight(method: string, headers: Record<string, unknown>): boolean {
  return method === 'OPTIONS' && typeof headers.origin === 'string' &&
    typeof headers['access-control-request-method'] === 'string';
}

export function mcpCorsPreflightResponse(): {
  statusCode: 204;
  headers: Record<string, string>;
} {
  return {
    statusCode: 204,
    headers: {
      'Access-Control-Allow-Origin': '*',
      'Access-Control-Allow-Methods': 'GET, POST, OPTIONS',
      'Access-Control-Allow-Headers': 'Authorization, Content-Type, Mcp-Session-Id',
    },
  };
}

export function rejectsUnauthenticatedRequest(
  mode: McpAuthMode,
  authenticated: boolean,
): boolean {
  return mode === "secured" && !authenticated;
}

export function mcpUnauthenticatedResponse(): {
  statusCode: 401;
  body: { ok: false; error: ReturnType<typeof createErrorEnvelope> };
} {
  return {
    statusCode: 401,
    body: { ok: false, error: createErrorEnvelope('KB_AUTH_REQUIRED', {
      details: { gatewayUrl: '<url>' }, correlationId: randomUUID(),
    }) },
  };
}
