import { describe, expect, it } from 'vitest';
import { getHostServiceAddress, HostServiceAddressError } from './host-service-address.js';

describe('getHostServiceAddress', () => {
  it.each([
    ['state-broker', 'KB_STATE_DAEMON_URL'],
    ['gateway', 'KB_GATEWAY_URL'],
  ] as const)('reads and validates the %s launch address', (service, variable) => {
    expect(getHostServiceAddress(service, { [variable]: 'http://127.0.0.1:43123' })).toBe('http://127.0.0.1:43123');
    expect(() => getHostServiceAddress(service, {})).toThrow(HostServiceAddressError);
    expect(() => getHostServiceAddress(service, { [variable]: 'file:///tmp/service' })).toThrow(HostServiceAddressError);
  });

  it('exposes the stable error code and an actionable hint', () => {
    try {
      getHostServiceAddress('state-broker', {});
      throw new Error('expected missing address error');
    } catch (error) {
      expect(error).toBeInstanceOf(HostServiceAddressError);
      expect(error).toMatchObject({
        code: 'KB_HOST_SERVICE_ADDRESS_MISSING',
        hint: expect.stringContaining('kb-create start'),
      });
    }
  });
});
