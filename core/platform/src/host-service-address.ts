export type HostServiceId = 'state-broker' | 'gateway';

export type HostServiceEnvironment = Partial<Record<
  'KB_STATE_DAEMON_URL' | 'KB_GATEWAY_URL',
  string | undefined
>>;

export class HostServiceAddressError extends Error {
  readonly code = 'KB_HOST_SERVICE_ADDRESS_MISSING';
  readonly hint: string;
  readonly reason: 'missing' | 'invalid';

  constructor(readonly service: HostServiceId, reason: 'missing' | 'invalid' = 'missing') {
    const label = service === 'state-broker' ? 'StateBroker' : 'gateway';
    super(reason === 'missing'
      ? `The host ${label} address was not passed to this runtime.`
      : `The host ${label} address passed to this runtime is not a valid HTTP URL.`);
    this.name = 'HostServiceAddressError';
    this.reason = reason;
    this.hint = 'Start the host with kb-create start so it can pass live host service addresses to this runtime.';
    Object.setPrototypeOf(this, HostServiceAddressError.prototype);
  }
}

/** Configuration input needed to address the installed platform's Gateway. */
export interface HostGatewayConfig {
  gateway?: {
    port?: unknown;
  };
}

/** A required gateway address could not be derived from effective platform config. */
export class HostGatewayAddressConfigError extends Error {
  constructor(
    readonly configPath: string,
    readonly field: string,
    detail: string,
  ) {
    super(`Cannot resolve the host gateway address from ${configPath}: ${detail}`);
    this.name = 'HostGatewayAddressConfigError';
    Object.setPrototypeOf(this, HostGatewayAddressConfigError.prototype);
  }
}

/**
 * Resolve the loopback address the installed host advertises to its runtimes.
 * The caller supplies the effective config it already loaded and the same
 * local network offset used by the host listener.
 */
export function resolveHostGatewayAddress(
  platformRoot: string | undefined,
  effectiveConfig: unknown,
  netOffset = 0,
): string {
  const configPath = platformRoot
    ? `${platformRoot.replace(/[\\/]+$/, '')}/.kb/kb.config.json`
    : '<platform-root>/.kb/kb.config.json';
  if (!platformRoot) {
    throw new HostGatewayAddressConfigError(
      configPath,
      'gateway.port',
      'the installed platform root was not resolved',
    );
  }
  if (!isRecord(effectiveConfig) || !isRecord(effectiveConfig.gateway)) {
    throw new HostGatewayAddressConfigError(
      configPath,
      'gateway.port',
      'the effective platform config has no gateway section',
    );
  }
  const port = effectiveConfig.gateway.port;
  if (typeof port !== 'number' || !Number.isInteger(port) || port < 1 || port > 65535) {
    throw new HostGatewayAddressConfigError(
      configPath,
      'gateway.port',
      'the effective platform config must set gateway.port to an integer from 1 to 65535',
    );
  }
  const resolvedPort = port + netOffset;
  if (!Number.isInteger(resolvedPort) || resolvedPort < 1 || resolvedPort > 65535) {
    throw new HostGatewayAddressConfigError(
      configPath,
      'gateway.port',
      `gateway.port plus KB_NET_OFFSET resolves to invalid port ${resolvedPort}`,
    );
  }
  return `http://127.0.0.1:${resolvedPort}`;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

const HOST_SERVICE_ENV: Record<HostServiceId, keyof HostServiceEnvironment> = {
  'state-broker': 'KB_STATE_DAEMON_URL',
  gateway: 'KB_GATEWAY_URL',
};

export function getHostServiceAddress(
  service: HostServiceId,
  environment: HostServiceEnvironment = process.env,
): string {
  const address = environment[HOST_SERVICE_ENV[service]];
  if (!address) {
    throw new HostServiceAddressError(service);
  }

  let parsed: URL;
  try {
    parsed = new URL(address);
  } catch {
    throw new HostServiceAddressError(service, 'invalid');
  }
  if ((parsed.protocol !== 'http:' && parsed.protocol !== 'https:') || !parsed.hostname) {
    throw new HostServiceAddressError(service, 'invalid');
  }
  return address.replace(/\/+$/, '');
}
