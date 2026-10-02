import { describe, expect, it } from 'vitest';
import { HostServiceAddressError } from '@kb-labs/core-platform';
import { getHostGatewayAddresses } from '../host-gateway-address.js';

describe('getHostGatewayAddresses', () => {
  it('uses the live host Gateway address', () => {
    expect(getHostGatewayAddresses({ KB_GATEWAY_URL: 'http://127.0.0.1:43123/' }))
      .toEqual({
        gatewayUrl: 'http://127.0.0.1:43123',
        dispatchEndpoint: 'http://127.0.0.1:43123/internal/dispatch',
      });
  });

  it('throws the typed host address error when the launch contract is missing', () => {
    let thrown: unknown;
    try {
      getHostGatewayAddresses({});
    } catch (error) {
      thrown = error;
    }
    expect(thrown).toBeInstanceOf(HostServiceAddressError);
    expect(thrown).toMatchObject({
      code: 'KB_HOST_SERVICE_ADDRESS_MISSING',
      hint: expect.any(String),
    });
  });
});
