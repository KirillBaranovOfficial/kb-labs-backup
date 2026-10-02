import {
  defineSystemCommand,
  type CommandResult,
} from "@kb-labs/shared-command-kit";
import { getContextCwd } from "@kb-labs/shared-cli-ui";
import {
  projectErrorEnvelope,
  showProject,
  createProjectRegistry,
} from "@kb-labs/core-project-registry";
import { failWithEnvelope } from "../project/project-support";

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
      if (json) {
        ctx.ui?.json({
          ok: false,
          error: { message, next: "kb mcp config <claude|codex|cursor>" },
        });
      } else {
        ctx.ui?.error(`${message} Next: kb mcp config <claude|codex|cursor>`);
      }
      return { ok: false, error: message };
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
      const port = Number(gateway?.port ?? process.env.GATEWAY_PORT ?? 4000);
      const origin = (
        process.env.GATEWAY_URL ?? `http://127.0.0.1:${port}`
      ).replace(/\/$/, "");
      const url = `${origin}/api/v1/projects/${encodeURIComponent(project.id)}/mcp`;
      const body = {
        ok: true as const,
        client,
        projectId: project.id,
        url,
        auth: local ? "none" : "Bearer ${KB_MCP_TOKEN}",
        config: renderMcpClientConfig(client, url, !local),
      };
      if (json) {
        ctx.ui?.json(body);
      } else {
        ctx.ui?.write(`${renderMcpClientConfig(client, url, !local)}\n`);
        if (!local) {
          ctx.ui?.write(
            "\nSet KB_MCP_TOKEN to a gateway-issued user or API token before starting the client.\n",
          );
        }
      }
      return body;
    } catch (error) {
      return failWithEnvelope(ctx.ui, projectErrorEnvelope(error), json);
    }
  },
});
