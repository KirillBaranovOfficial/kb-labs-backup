import { afterEach, describe, expect, it } from 'vitest';
import { BulkTransferHelper } from '../transport/bulk-transfer.js';

const originalSigintListeners = process.listeners('SIGINT');
const originalSigtermListeners = process.listeners('SIGTERM');

afterEach(() => {
  for (const listener of process.listeners('SIGINT')) {
    if (!originalSigintListeners.includes(listener)) {
      process.removeListener('SIGINT', listener);
    }
  }
  for (const listener of process.listeners('SIGTERM')) {
    if (!originalSigtermListeners.includes(listener)) {
      process.removeListener('SIGTERM', listener);
    }
  }
});

describe('BulkTransferHelper process handlers', () => {
  it('does not install a process-wide uncaught exception handler', () => {
    const originalUncaughtListeners = process.listeners('uncaughtException');

    BulkTransferHelper.registerSignalHandlers();

    expect(process.listeners('uncaughtException')).toEqual(originalUncaughtListeners);
  });
});
