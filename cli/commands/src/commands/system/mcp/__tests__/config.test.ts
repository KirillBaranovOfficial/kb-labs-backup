import { describe, expect, it } from "vitest";
import { renderMcpClientConfig } from "../config.js";

describe("MCP client configuration", () => {
  const url = "http://127.0.0.1:4000/api/v1/projects/prj_demo/mcp";

  it("prints tokenless HTTP configs for local installs", () => {
    expect(JSON.parse(renderMcpClientConfig("claude", url, false))).toEqual({
      mcpServers: { "kb-labs": { type: "http", url } },
    });
    expect(renderMcpClientConfig("codex", url, false)).toBe(
      `[mcp_servers.kb-labs]\nurl = "${url}"`,
    );
  });

  it("uses the gateway token environment variable in secured client configs", () => {
    expect(JSON.parse(renderMcpClientConfig("cursor", url, true))).toEqual({
      mcpServers: {
        "kb-labs": {
          url,
          headers: { Authorization: "Bearer ${KB_MCP_TOKEN}" },
        },
      },
    });
    expect(renderMcpClientConfig("codex", url, true)).toContain(
      'bearer_token_env_var = "KB_MCP_TOKEN"',
    );
  });
});
