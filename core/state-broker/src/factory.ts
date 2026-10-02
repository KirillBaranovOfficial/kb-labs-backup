/**
 * State broker factory
 */

import type { StateBroker, StateBrokerOptions } from './index';
import { getHostServiceAddress } from '@kb-labs/core-platform';
import { InMemoryStateBroker } from './backends/in-memory';
import { HTTPStateBroker } from './backends/http';

/**
 * Create state broker instance
 */
export function createStateBroker(options?: StateBrokerOptions): StateBroker {
  const backend = options?.backend ?? 'memory';

  switch (backend) {
    case 'http':
      return new HTTPStateBroker();
    case 'memory':
    default:
      return new InMemoryStateBroker();
  }
}

/**
 * Detect available state broker
 * Tries HTTP first, falls back to memory
 */
export async function detectStateBroker(): Promise<StateBroker> {
  const address = getHostServiceAddress('state-broker');
  try {
    // Try to ping daemon
    const res = await fetch(`${address}/health`, {
      signal: AbortSignal.timeout(1000), // 1s timeout
    });

    if (res.ok) {
      // Daemon available - use HTTP backend
      return new HTTPStateBroker({ KB_STATE_DAEMON_URL: address });
    }
  } catch {
    // Daemon not available or timeout
  }

  // Fallback to in-memory
  return new InMemoryStateBroker();
}
