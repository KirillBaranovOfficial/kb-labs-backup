import { describe, expect, it } from 'vitest';
import { getHostServiceAddress, HostServiceAddressError } from './host-service-address.js';
import { resolveHostGatewayAddress } from './host-service-address.js';
import { toErrorEnvelope } from './error-envelope/index.js';

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

  // case: S-010.7
  it('resolves the configured public gateway address with the local network offset', () => {
    expect(resolveHostGatewayAddress('/opt/kb-platform', {
      gateway: { port: 43123 },
    }, 200)).toBe('http://127.0.0.1:43323');
  });

  // case: S-203.15
  it('reports the missing platform gateway port with a catalogued config hint', () => {
    try {
      resolveHostGatewayAddress('/opt/kb-platform', {}, 0);
      throw new Error('expected missing gateway config failure');
    } catch (error) {
      expect(toErrorEnvelope(error)).toMatchObject({
        code: 'KB_CONFIG_INVALID',
        message: expect.stringContaining('/opt/kb-platform/.kb/kb.config.json'),
        hint: expect.stringContaining('kb config set --scope platform gateway.port'),
      });
      expect(toErrorEnvelope(error).message).toContain('gateway.port');
    }
  });
});
