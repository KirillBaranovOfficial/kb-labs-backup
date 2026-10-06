import { describe, expect, it } from 'vitest';
import { toErrorEnvelope } from './error-envelope/index.js';
import {
  GATEWAY_SERVICE_NETWORK,
  resolveGatewayServiceAddress,
  resolveServiceListenAddress,
} from './service-listen-address.js';

describe('resolveServiceListenAddress', () => {
  it('exports the gateway service network settings shared by all address consumers', () => {
    expect(GATEWAY_SERVICE_NETWORK).toEqual({
      serviceId: 'gateway',
      portEnvVar: 'GATEWAY_PORT',
      defaultPort: 4000,
      hostEnvVar: 'GATEWAY_HOST',
      defaultHost: '0.0.0.0',
    });
  });

  it('resolves the gateway URL from the host override before listener settings', () => {
    expect(resolveGatewayServiceAddress(undefined, {
      gatewayUrl: 'https://gateway.example.test/',
      netOffset: 500,
    })).toBe('https://gateway.example.test');
  });

  it('uses the same transport, GATEWAY_PORT, and offset rules as plugin Gateway calls', () => {
    expect(resolveGatewayServiceAddress(undefined, {
      gatewayPort: '4300',
      netOffset: 200,
    })).toBe('http://127.0.0.1:4500');
    expect(resolveGatewayServiceAddress({
      listenAddress: () => ({ host: '0.0.0.0', port: 43323 }),
    }, {
      gatewayPort: '4300',
      netOffset: 200,
    })).toBe('http://127.0.0.1:43323');
  });

  it('surfaces invalid GATEWAY_PORT as the catalogued configuration error', () => {
    let failure: unknown;
    try {
      resolveGatewayServiceAddress(undefined, { gatewayPort: '4000oops', netOffset: 0 });
    } catch (error) {
      failure = error;
    }
    expect(toErrorEnvelope(failure)).toMatchObject({ code: 'KB_CONFIG_INVALID' });
  });

  // case: S-010.7
  it('uses the service transport listen address without applying the offset twice', () => {
    expect(resolveServiceListenAddress({
      listenAddress: () => ({ host: '10.0.0.8', port: 43323 }),
    }, {
      serviceId: 'gateway',
      defaultHost: '0.0.0.0',
      defaultPort: 4000,
    }, 200)).toEqual({ host: '10.0.0.8', port: 43323 });
  });

  // case: S-203.15
  it('uses the module default and offset when a fresh install has no transport address or gateway.port', () => {
    expect(resolveServiceListenAddress(undefined, {
      serviceId: 'gateway',
      defaultHost: '0.0.0.0',
      defaultPort: 4000,
    }, 200)).toEqual({ host: '0.0.0.0', port: 4200 });
  });

  it('uses the module port environment value and offset when transport has no TCP address', () => {
    expect(resolveServiceListenAddress({
      listenAddress: () => ({ socketPath: '/tmp/gateway.sock' }),
    }, {
      serviceId: 'gateway',
      portEnvValue: '4500',
      defaultPort: 4000,
      hostEnvValue: '127.0.0.1',
      defaultHost: '0.0.0.0',
    }, 200)).toEqual({ host: '127.0.0.1', port: 4700 });
  });

  // case: S-203.16
  it('rejects malformed and out-of-range GATEWAY_PORT values with a catalogued config error', () => {
    for (const value of ['4000oops', '4000.5', '0', '65536', '-1']) {
      let failure: unknown;
      try {
        resolveServiceListenAddress(undefined, {
          serviceId: GATEWAY_SERVICE_NETWORK.serviceId,
          portEnvVar: GATEWAY_SERVICE_NETWORK.portEnvVar,
          portEnvValue: value,
          defaultPort: GATEWAY_SERVICE_NETWORK.defaultPort,
        }, 0);
      } catch (error) {
        failure = error;
      }

      expect(toErrorEnvelope(failure)).toMatchObject({
        code: 'KB_CONFIG_INVALID',
        message: expect.stringContaining('GATEWAY_PORT'),
        hint: 'Run "unset GATEWAY_PORT", then retry the command to use the service default port.',
      });
    }
  });

  // case: S-203.16
  it('rejects an offset that would put the resolved listener outside the valid port range', () => {
    let failure: unknown;
    try {
      resolveServiceListenAddress(undefined, {
        serviceId: 'gateway',
        portEnvVar: 'GATEWAY_PORT',
        portEnvValue: '65530',
        defaultPort: 4000,
      }, 10);
    } catch (error) {
      failure = error;
    }

    expect(toErrorEnvelope(failure)).toMatchObject({
      code: 'KB_CONFIG_INVALID',
      message: expect.stringContaining('GATEWAY_PORT'),
    });
  });
});
