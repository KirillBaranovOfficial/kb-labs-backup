import { describe, expect, it } from 'vitest';
import { resolveServiceListenAddress } from './service-listen-address.js';

describe('resolveServiceListenAddress', () => {
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
});
