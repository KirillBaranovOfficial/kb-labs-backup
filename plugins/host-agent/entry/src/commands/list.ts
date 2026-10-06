/**
 * workspace:list command
 * List all connected Workspace Agents via Gateway REST API.
 */

import { defineCommand, getHostServiceAddress, handleError, type PluginCommandContextV3, type CommandResult } from '@kb-labs/sdk';
import { GatewayApiError, gatewayApiFetch } from '@kb-labs/cli-runtime/gateway';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { homedir } from 'node:os';

type ListInput = {
  json?: boolean;
  gateway?: string;
  flags?: ListInput;
};

interface HostEntry {
  hostId: string;
  name: string;
  status: string;
  capabilities: string[];
  hostType?: string;
  lastSeen: number;
  connections: string[];
}

type ListResult = { hosts: HostEntry[] };

export default defineCommand({
  id: 'workspace:list',
  description: 'List all connected Workspace Agents',

  handler: {
    async execute(ctx: PluginCommandContextV3, rawInput: ListInput): Promise<CommandResult<ListResult>> {
      const input: ListInput = rawInput.flags ?? rawInput;

      // Resolve Gateway URL: flag > agent config > env > default
      let gatewayUrl = input.gateway;
      if (!gatewayUrl) {
        try {
          const configPath = join(homedir(), '.kb', 'agent.json');
          const config = JSON.parse(readFileSync(configPath, 'utf-8'));
          gatewayUrl = config.gatewayUrl;
        } catch {
          // No agent config
        }
      }
      gatewayUrl = gatewayUrl ?? getHostServiceAddress('gateway', {
        KB_GATEWAY_URL: ctx.runtime?.env?.('KB_GATEWAY_URL'),
      });

      let hosts: HostEntry[] = [];
      try {
        const res = await gatewayApiFetch(`${gatewayUrl}/hosts`);
        if (res.ok) {
          const body = await res.json() as { hosts: HostEntry[] };
          hosts = body.hosts;
        } else {
          return handleError(ctx, new Error(`Failed to list hosts: HTTP ${res.status}`), input.json);
        }
      } catch (err) {
        if (err instanceof GatewayApiError) {
          return handleError(ctx, err, input.json);
        }
        return handleError(ctx, new Error(`Cannot reach Gateway at ${gatewayUrl}: ${err instanceof Error ? err.message : String(err)}`), input.json);
      }

      if (input.json) {
        return { ok: true, result: { hosts } };
      }

      if (hosts.length === 0) {
        ctx.ui?.info?.('No Workspace Agents connected.');
        return { ok: true, result: { hosts: [] } };
      }

      const ago = (ts: number) => {
        const sec = Math.floor((Date.now() - ts) / 1000);
        if (sec < 60) {return `${sec}s ago`;}
        if (sec < 3600) {return `${Math.floor(sec / 60)}m ago`;}
        if (sec < 86400) {return `${Math.floor(sec / 3600)}h ago`;}
        return `${Math.floor(sec / 86400)}d ago`;
      };

      const onlineCount = hosts.filter(h => h.status === 'online').length;
      const offlineCount = hosts.filter(h => h.status === 'offline').length;

      ctx.ui?.success?.(`${onlineCount} online, ${offlineCount} offline (${hosts.length} total)`, {
        sections: [{
          header: 'Agents',
          items: hosts.map(h =>
            `${h.name}  ${h.status}  ${(h.capabilities || []).join(', ') || '—'}  ${ago(h.lastSeen)}`
          ),
        }],
      });

      return { ok: true, result: { hosts } };
    },
  },
});
