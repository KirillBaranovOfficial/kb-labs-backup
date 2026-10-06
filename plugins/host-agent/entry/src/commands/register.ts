/**
 * workspace:register command
 * Register this machine with a Platform Gateway and save credentials to ~/.kb/agent.json.
 */

import { defineCommand, validationError, handleError, type PluginCommandContextV3, type CLIInput, type CommandResult } from '@kb-labs/sdk';
import { GatewayApiError, gatewayApiFetch } from '@kb-labs/cli-runtime/gateway';
import { writeFile, mkdir, stat } from 'node:fs/promises';
import { join, resolve, normalize } from 'node:path';
import { homedir, hostname } from 'node:os';

interface RegisterFlags {
  gateway?: string;
  name?: string;
  workspace?: string[];
  namespace?: string;
  json?: boolean;
  'dry-run'?: boolean;
}

type RegisterResult = {
  configPath?: string;
  hostId?: string;
  clientId?: string;
};

export default defineCommand({
  id: 'workspace:register',
  description: 'Register this machine with a Platform Gateway',

  handler: {
    async intent(_ctx: PluginCommandContextV3, input: CLIInput<RegisterFlags>) {
      const flags = input.flags;
      const gatewayUrl = (flags.gateway ?? '').replace(/\/$/, '');
      const name = flags.name ?? '(this machine)';
      return {
        summary: `Register workspace agent with Gateway ${gatewayUrl || '(unknown)'}`,
        operations: [
          { type: 'create' as const, resource: 'agent-credentials', details: { name, gateway: gatewayUrl } },
          { type: 'create' as const, resource: 'file', details: { path: '~/.kb/agent.json' } },
        ],
      };
    },

    async execute(ctx: PluginCommandContextV3, input: CLIInput<RegisterFlags>): Promise<CommandResult<RegisterResult>> {
      const flags = input.flags;
      const gatewayUrl = (flags.gateway ?? '').replace(/\/$/, '');
      const name = flags.name ?? hostname();
      const namespaceId = flags.namespace ?? 'default';
      const rawWorkspace = flags.workspace;
      const rawPaths = rawWorkspace
        ? (Array.isArray(rawWorkspace) ? rawWorkspace : [rawWorkspace as unknown as string])
        : [process.cwd()];

      // Resolve and validate workspace paths: must be absolute directories, no traversal
      const workspacePaths: string[] = [];
      for (const p of rawPaths) {
        const resolved = resolve(normalize(p));
        let pathStat: Awaited<ReturnType<typeof stat>> | null = null;
        try { pathStat = await stat(resolved); } catch { /* will error below */ }
        if (!pathStat?.isDirectory()) {
          return validationError(ctx, `Workspace path does not exist or is not a directory: ${resolved}`, undefined, flags.json);
        }
        workspacePaths.push(resolved);
      }

      if (!gatewayUrl) {
        return validationError(ctx, '--gateway is required', 'Usage: kb workspace:register --gateway http://localhost:4000', flags.json);
      }

      // Validate URL
      if (!gatewayUrl.startsWith('http://') && !gatewayUrl.startsWith('https://')) {
        return validationError(ctx, 'Invalid gateway URL — must start with http:// or https://', undefined, flags.json);
      }

      ctx.ui?.info?.(`Registering with Gateway: ${gatewayUrl}`);

      // POST /auth/register
      let res: Response;
      try {
        res = await gatewayApiFetch(`${gatewayUrl}/auth/register`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ name, namespaceId, capabilities: ['filesystem', 'git', 'execution', 'search', 'shell'] }),
        });
      } catch (err) {
        if (err instanceof GatewayApiError) {
          return handleError(ctx, err, flags.json);
        }
        const causeMessage = err instanceof Error ? err.message : String(err);
        return handleError(ctx, new Error(`Failed to reach Gateway: ${causeMessage}`), flags.json);
      }

      if (!res.ok) {
        const message = `Gateway returned HTTP ${res.status}. Check the gateway and retry kb workspace:register --gateway ${gatewayUrl}.`;
        return handleError(ctx, new Error(message), flags.json);
      }

      let data: { clientId?: string; clientSecret?: string; hostId?: string };
      try {
        data = await res.json() as typeof data;
      } catch {
        return handleError(ctx, new Error('Gateway response is not valid JSON'), flags.json);
      }

      if (!data.clientId || !data.clientSecret || !data.hostId) {
        return handleError(ctx, new Error('Gateway response is missing required fields (clientId, clientSecret, hostId)'), flags.json);
      }

      // Format validation — secrets must be non-empty strings with safe characters only
      const SECRET_PATTERN = /^[A-Za-z0-9_\-+/=]{16,}$/;
      if (typeof data.clientSecret !== 'string' || !SECRET_PATTERN.test(data.clientSecret)) {
        return handleError(ctx, new Error('Gateway returned an invalid client secret (unexpected format)'), flags.json);
      }

      // Write ~/.kb/agent.json
      const kbDir = join(homedir(), '.kb');
      await mkdir(kbDir, { recursive: true });
      const configPath = join(kbDir, 'agent.json');

      const agentConfig = {
        clientId: data.clientId,
        clientSecret: data.clientSecret,
        hostId: data.hostId,
        gatewayUrl,
        namespaceId,
        hostType: 'local' as const,
        workspacePaths,
        execution: {
          mode: 'in-process' as const,
          timeoutMs: 120_000,
        },
      };

      await writeFile(configPath, JSON.stringify(agentConfig, null, 2), { mode: 0o600 });

      if (flags.json) {
        return { ok: true, result: { configPath, hostId: data.hostId, clientId: data.clientId } };
      } else {
        ctx.ui?.success?.('Workspace Agent registered', {
          sections: [{
            items: [
              `Host ID:    ${data.hostId}`,
              `Client ID:  ${data.clientId}`,
              `Gateway:    ${gatewayUrl}`,
              `Namespace:  ${namespaceId}`,
              `Workspaces: ${workspacePaths.join(', ')}`,
              `Config:     ${configPath}`,
            ],
          }],
          hint: 'Next: start the daemon with `kb-dev start host-agent` or `kb workspace:start`.',
        });
      }

      return { ok: true, result: { configPath } };
    },
  },
});
