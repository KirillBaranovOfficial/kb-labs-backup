export type McpAuthMode = "local" | "secured";
import { randomUUID } from 'node:crypto';
import { createErrorEnvelope } from '@kb-labs/core-platform';
import { createUxFailureResult } from '@kb-labs/core-platform/ux-result';

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

export function mcpUnauthenticatedResponse(
  gatewayUrl = '<url>',
  reason: 'missing-identity' | 'unsupported-credential' = 'missing-identity',
): {
  statusCode: 401;
  body: ReturnType<typeof createUxFailureResult>;
} {
  const code = reason === 'unsupported-credential' ? 'KB_AUTH_MCP_CREDENTIAL_REQUIRED' : 'KB_AUTH_REQUIRED';
  return {
    statusCode: 401,
    body: createUxFailureResult(createErrorEnvelope(code, {
      details: { gatewayUrl }, correlationId: randomUUID(),
    })),
  };
}
