/**
 * Integration tests for HTTPStateBroker
 *
 * Tests graceful degradation when daemon is unavailable.
 * For tests with daemon running, see state-daemon integration tests.
 */

import { afterEach, describe, it, expect } from 'vitest';
import { HTTPStateBroker } from '../backends/http.js';
import { createStateBroker, detectStateBroker } from '../factory.js';
import { HostServiceAddressError } from '@kb-labs/core-platform';

const originalAddress = process.env.KB_STATE_DAEMON_URL;

afterEach(() => {
  if (originalAddress === undefined) {
    delete process.env.KB_STATE_DAEMON_URL;
  } else {
    process.env.KB_STATE_DAEMON_URL = originalAddress;
  }
});

describe('HTTPStateBroker - Graceful Degradation', () => {
  it('requires the host-provided address for HTTP factory and detection', async () => {
    delete process.env.KB_STATE_DAEMON_URL;
    expect(() => createStateBroker({ backend: 'http' })).toThrow(HostServiceAddressError);
    await expect(detectStateBroker()).rejects.toBeInstanceOf(HostServiceAddressError);
  });

  describe('Daemon Unavailable', () => {
    it('should return null when daemon is down (get)', async () => {
      // Connect to non-existent daemon
      const broker = new HTTPStateBroker({ KB_STATE_DAEMON_URL: 'http://localhost:9999' });

      const result = await broker.get('test-key');

      // Should return null instead of throwing
      expect(result).toBeNull();
    });

    it('should silently fail when daemon is down (set)', async () => {
      const broker = new HTTPStateBroker({ KB_STATE_DAEMON_URL: 'http://localhost:9999' });

      // Should not throw
      await expect(broker.set('key', 'value', 60_000)).resolves.toBeUndefined();
    });

    it('should silently fail when daemon is down (delete)', async () => {
      const broker = new HTTPStateBroker({ KB_STATE_DAEMON_URL: 'http://localhost:9999' });

      // Should not throw
      await expect(broker.delete('key')).resolves.toBeUndefined();
    });

    it('should silently fail when daemon is down (clear)', async () => {
      const broker = new HTTPStateBroker({ KB_STATE_DAEMON_URL: 'http://localhost:9999' });

      // Should not throw
      await expect(broker.clear()).resolves.toBeUndefined();
    });

    it('should throw on malformed URL', async () => {
      expect(() => new HTTPStateBroker({ KB_STATE_DAEMON_URL: 'not-a-valid-url' })).toThrow(HostServiceAddressError);
    });
  });

  describe('URL Encoding', () => {
    it('should encode special characters in keys', async () => {
      const broker = new HTTPStateBroker({ KB_STATE_DAEMON_URL: 'http://localhost:9999' });

      // Keys with special chars
      const specialKey = 'tenant:acme:mind:query-123@#$%^&*()';

      // Should not throw during encoding
      await expect(broker.get(specialKey)).resolves.toBeNull();
      await expect(broker.set(specialKey, 'value')).resolves.toBeUndefined();
      await expect(broker.delete(specialKey)).resolves.toBeUndefined();
    });

    it('should encode pattern in clear', async () => {
      const broker = new HTTPStateBroker({ KB_STATE_DAEMON_URL: 'http://localhost:9999' });

      const pattern = 'mind:*@#$';

      // Should not throw
      await expect(broker.clear(pattern)).resolves.toBeUndefined();
    });
  });

  describe('Stop Operation', () => {
    it('should stop without errors', async () => {
      const broker = new HTTPStateBroker({ KB_STATE_DAEMON_URL: 'http://localhost:9999' });

      await expect(broker.stop()).resolves.toBeUndefined();
    });
  });
});

/**
 * Note: Tests requiring running daemon are in state-daemon package tests.
 * These tests focus on graceful degradation and client-side logic only.
 */
