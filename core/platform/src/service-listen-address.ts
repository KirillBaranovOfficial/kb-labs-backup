import type { IServiceTransport } from './adapters/service-transport.js';
import { ServicePortConfigError } from './errors.js';
import { getHostServiceAddress } from './host-service-address.js';

/** Canonical listener settings for the Gateway service across all launchers. */
export const GATEWAY_SERVICE_NETWORK = {
  serviceId: 'gateway',
  portEnvVar: 'GATEWAY_PORT',
  defaultPort: 4000,
  hostEnvVar: 'GATEWAY_HOST',
  defaultHost: '0.0.0.0',
} as const;

/** Network inputs needed by both the daemon binder and address consumers. */
export interface ServiceNetworkSettings {
  serviceId: string;
  portEnvVar?: string;
  portEnvValue?: string;
  defaultPort?: number;
  hostEnvValue?: string;
  defaultHost?: string;
}

export interface ResolvedServiceListenAddress {
  port: number;
  host: string;
}

export interface GatewayServiceAddressOptions {
  gatewayUrl?: string;
  gatewayPort?: string;
  gatewayHost?: string;
  netOffset: number;
}

/** Resolve the HTTP URL that CLI children use to reach the Gateway listener. */
export function resolveGatewayServiceAddress(
  transport: Pick<IServiceTransport, 'listenAddress'> | undefined,
  options: GatewayServiceAddressOptions,
): string {
  if (options.gatewayUrl) {
    return getHostServiceAddress('gateway', { KB_GATEWAY_URL: options.gatewayUrl });
  }
  const address = resolveServiceListenAddress(transport, {
    serviceId: GATEWAY_SERVICE_NETWORK.serviceId,
    portEnvVar: GATEWAY_SERVICE_NETWORK.portEnvVar,
    portEnvValue: options.gatewayPort,
    defaultPort: GATEWAY_SERVICE_NETWORK.defaultPort,
    hostEnvValue: options.gatewayHost,
    defaultHost: GATEWAY_SERVICE_NETWORK.defaultHost,
  }, options.netOffset);
  return `http://127.0.0.1:${address.port}`;
}

/**
 * Resolve the exact service bind address used by `@kb-labs/shared-daemon`.
 * Transport addresses already include their network offset. Port environment
 * values and module defaults receive the offset here, matching daemon startup.
 */
export function resolveServiceListenAddress(
  transport: Pick<IServiceTransport, 'listenAddress'> | undefined,
  settings: ServiceNetworkSettings,
  netOffset: number,
): ResolvedServiceListenAddress {
  const address = transport?.listenAddress?.(settings.serviceId);
  const port = address && 'port' in address
    ? address.port
    : resolveConfiguredPort(settings, netOffset);
  const transportHost = address && 'host' in address ? address.host : undefined;
  const host = settings.hostEnvValue || transportHost || settings.defaultHost || '0.0.0.0';
  return { port, host };
}

function resolveConfiguredPort(settings: ServiceNetworkSettings, netOffset: number): number {
  const envVar = settings.portEnvVar ?? `service.${settings.serviceId}.port`;
  const configuredPort = settings.portEnvValue;
  let port: number | undefined;
  if (configuredPort !== undefined) {
    if (!/^\d+$/.test(configuredPort)) {
      throw new ServicePortConfigError(envVar, 'must be an integer from 1 through 65535');
    }
    port = Number(configuredPort);
    if (!Number.isSafeInteger(port) || port < 1 || port > 65535) {
      throw new ServicePortConfigError(envVar, 'must be an integer from 1 through 65535');
    }
  } else {
    port = settings.defaultPort;
  }
  if (port === undefined) {
    throw new Error(
      `Service transport has no listen address for "${settings.serviceId}" and no port was configured.`,
    );
  }
  const resolvedPort = port + netOffset;
  if (!Number.isSafeInteger(resolvedPort) || resolvedPort < 1 || resolvedPort > 65535) {
    throw new ServicePortConfigError(
      envVar,
      `must resolve to an integer from 1 through 65535 after applying the network offset (resolved ${resolvedPort})`,
    );
  }
  return resolvedPort;
}
