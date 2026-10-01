import { describe, expect, it } from "vitest";
import { isMcpCorsPreflight, mcpCorsPreflightResponse, mcpUnauthenticatedResponse, rejectsUnauthenticatedRequest } from "../mcp/auth-mode.js";

describe("MCP access mode", () => {
  it("allows tokenless requests in local mode", () => {
    expect(rejectsUnauthenticatedRequest("local", false)).toBe(false);
  });

  it("requires a verified gateway token in secured mode", () => {
    expect(rejectsUnauthenticatedRequest("secured", false)).toBe(true);
    expect(rejectsUnauthenticatedRequest("secured", true)).toBe(false);
  });

  it('returns HTTP 401 with the catalog envelope before MCP protocol handling', () => {
    expect(mcpUnauthenticatedResponse()).toMatchObject({
      statusCode: 401,
      body: {
        error: {
          code: 'KB_AUTH_REQUIRED',
          message: 'Authentication is required.',
          hint: 'Run "kb auth login".',
        },
      },
    });
  });

  it('recognizes CORS preflight without treating ordinary OPTIONS as preflight', () => {
    expect(isMcpCorsPreflight('OPTIONS', {
      origin: 'https://studio.example.test',
      'access-control-request-method': 'POST',
    })).toBe(true);
    expect(isMcpCorsPreflight('OPTIONS', {})).toBe(false);
    expect(isMcpCorsPreflight('GET', {
      origin: 'https://studio.example.test',
      'access-control-request-method': 'POST',
    })).toBe(false);
    expect(mcpCorsPreflightResponse()).toEqual({
      statusCode: 204,
      headers: {
        'Access-Control-Allow-Origin': '*',
        'Access-Control-Allow-Methods': 'GET, POST, OPTIONS',
        'Access-Control-Allow-Headers': 'Authorization, Content-Type, Mcp-Session-Id',
      },
    });
  });
});
