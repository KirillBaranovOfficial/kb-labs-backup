import {
  getHostServiceAddress,
  type HostServiceEnvironment,
} from '@kb-labs/core-platform';

export interface HostGatewayAddresses {
  gatewayUrl: string;
  dispatchEndpoint: string;
}

export function getHostGatewayAddresses(
  environment?: HostServiceEnvironment,
): HostGatewayAddresses {
  const gatewayUrl = getHostServiceAddress('gateway', environment);
  return {
    gatewayUrl,
    dispatchEndpoint: `${gatewayUrl}/internal/dispatch`,
  };
}
