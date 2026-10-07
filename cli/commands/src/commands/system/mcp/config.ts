import { defineSystemCommand, throwSystemCommandError,
  type CommandResult,
} from "@kb-labs/shared-command-kit";
import { getContextCwd } from "@kb-labs/shared-cli-ui";
import {
  projectErrorEnvelope,
  showProject,
  createProjectRegistry,
} from "@kb-labs/core-project-registry";
import { failWithEnvelope } from "../project/project-support";
import { resolveActiveGateway } from '@kb-labs/cli-runtime/gateway';
import { SessionManager } from '@kb-labs/cli-runtime/gateway';

type Client = "claude" | "codex" | "cursor";
type ConfigFlags = { json: { type: "boolean"; description?: string } };

function effectiveConfig(): Record<string, unknown> {
  return (
    (
      globalThis as typeof globalThis & {
        __KB_EFFECTIVE_CONFIG__?: Record<string, unknown>;
      }
    ).__KB_EFFECTIVE_CONFIG__ ?? {}
  );
}

export function renderMcpClientConfig(
  client: Client,
  url: string,
  secured: boolean,
): string {
  const headers = secured
    ? { Authorization: "Bearer ${KB_MCP_TOKEN}" }
    : undefined;
  if (client === "claude") {
    return JSON.stringify(
      {
        mcpServers: {
          "kb-labs": { type: "http", url, ...(headers ? { headers } : {}) },
        },
      },
      null,
      2,
    );
  }
  if (client === "cursor") {
    return JSON.stringify(
      { mcpServers: { "kb-labs": { url, ...(headers ? { headers } : {}) } } },
      null,
      2,
    );
  }
  return [
    "[mcp_servers.kb-labs]",
    `url = ${JSON.stringify(url)}`,
    ...(secured ? ['bearer_token_env_var = "KB_MCP_TOKEN"'] : []),
  ].join("\n");
}

export const mcpConfig = defineSystemCommand<ConfigFlags, CommandResult>({
  name: "config",
  scope: 'project',
  jsonTest: { safeArgs: ["claude"] },
  description: "Print MCP client configuration for the current project",
  longDescription:
    "Prints a ready-to-copy HTTP MCP configuration for Claude Code, Codex, or Cursor. In secured mode, set KB_MCP_TOKEN to a gateway-issued user or API token.",
  category: "mcp",
  examples: [
    "kb mcp config claude",
    "kb mcp config codex",
    "kb mcp config cursor",
  ],
  flags: {
    json: { type: "boolean", description: "Output machine-readable JSON" },
  },
  analytics: {
    command: "mcp.config",
    startEvent: "MCP_CONFIG_STARTED",
    finishEvent: "MCP_CONFIG_FINISHED",
  },
  async handler(ctx, argv, flags) {
    const json = Boolean(flags.json);
    const client = argv[0];
    if (client !== "claude" && client !== "codex" && client !== "cursor") {
      const message = "Choose a client: claude, codex, or cursor.";
      throwSystemCommandError(message, "Run `kb mcp config --help` to review valid inputs and recovery steps, then retry.");
    }
    try {
      const cwd = getContextCwd(ctx);
      const project = await showProject(
        createProjectRegistry({ env: process.env }),
        cwd,
      );
      const effective = effectiveConfig();
      const config = (
        effective.data && typeof effective.data === "object"
          ? effective.data
          : effective
      ) as Record<string, unknown>;
      const gateway = config.gateway as Record<string, unknown> | undefined;
      const host = config.host as Record<string, unknown> | undefined;
      const access = gateway?.access as Record<string, unknown> | undefined;
      const local = access?.mode === "local" || host?.auth === "off";
      const gatewayTarget = await resolveActiveGateway({ cwd, env: process.env, configuredGatewayPort: gateway?.port !== undefined ? String(gateway.port) : undefined });
      const origin = gatewayTarget.gatewayUrl;
      const url = `${origin}/api/v1/projects/${encodeURIComponent(project.id)}/mcp`;
      const body = {
        client,
        projectId: project.id,
        url,
        auth: local ? "none" : "Bearer ${KB_MCP_TOKEN}",
        config: renderMcpClientConfig(client, url, !local),
      };
      if (!json) {
        ctx.ui?.write(`${renderMcpClientConfig(client, url, !local)}\n`);
        if (!local) {
          ctx.ui?.write(
            "\nSet KB_MCP_TOKEN to a gateway-issued user or API token before starting the client.\n",
          );
        }
      }
      return body;
    } catch (error) {
      return failWithEnvelope(projectErrorEnvelope(error));
    }
  },
});
