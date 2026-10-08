import { loadPlatformConfig } from '@kb-labs/core-runtime';
import { resolveCliGatewayTarget, type IServiceTransport, type CliGatewayTarget } from '@kb-labs/core-platform';
import { AuthSessionOwner } from './auth-session.js';

export interface ResolveActiveGatewayOptions {
  cwd: string;
  env: NodeJS.ProcessEnv;
  platformRoot?: string;
  transport?: Pick<IServiceTransport, 'listenAddress'>;
  configuredGatewayPort?: string;
  configuredNetOffset?: number;
}

/** Resolve the active platform root, its Gateway address, and its saved Gateway identity. */
export async function resolveActiveGateway({
  cwd,
  env,
  platformRoot: explicitPlatformRoot,
  transport,
  configuredGatewayPort,
  configuredNetOffset,
}: ResolveActiveGatewayOptions): Promise<CliGatewayTarget> {
  const platformRoot = explicitPlatformRoot ?? (await loadPlatformConfig({
    startDir: cwd,
    env,
    loadEnvFile: false,
    projectScope: 'platform',
  })).platformRoot;
  const { session, machine } = await new AuthSessionOwner().inspect();
  return resolveCliGatewayTarget(platformRoot, {
    gatewayUrl: env.KB_GATEWAY_URL,
    sessionGatewayUrl: session?.gatewayUrl ?? machine?.gatewayUrl,
    gatewayPort: env.GATEWAY_PORT ?? configuredGatewayPort,
    gatewayHost: env.GATEWAY_HOST,
    netOffset: env.KB_NET_OFFSET === undefined ? configuredNetOffset ?? 0 : Number(env.KB_NET_OFFSET),
  }, transport);
}
