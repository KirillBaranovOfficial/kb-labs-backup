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
