import type { IServiceTransport } from './adapters/service-transport.js';

/** Network inputs needed by both the daemon binder and address consumers. */
export interface ServiceNetworkSettings {
  serviceId: string;
  portEnvValue?: string;
  defaultPort?: number;
  hostEnvValue?: string;
  defaultHost?: string;
}

export interface ResolvedServiceListenAddress {
  port: number;
  host: string;
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
  const configuredPort = settings.portEnvValue;
  const port = configuredPort ? parseInt(configuredPort, 10) : settings.defaultPort;
  if (port === undefined) {
    throw new Error(
      `Service transport has no listen address for "${settings.serviceId}" and no port was configured.`,
    );
  }
  return port + netOffset;
}
