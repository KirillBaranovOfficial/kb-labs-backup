import { describe, expect, it, vi } from 'vitest';
import { PinoLoggerAdapter } from './index.js';

describe('PinoLoggerAdapter', () => {
  it('includes child bindings in onLog records', () => {
    const logger = new PinoLoggerAdapter({ level: 'debug' });
    const callback = vi.fn();
    logger.onLog(callback);

    const child = logger.child({
      traceId: 'trace-1',
      requestId: 'req-1',
      invocationId: 'inv-1',
    });

    child.info('hello', { step: 'start' });

    expect(callback).toHaveBeenCalledTimes(1);
    const [record] = callback.mock.calls[0]!;
    expect(record.fields).toMatchObject({
      traceId: 'trace-1',
      requestId: 'req-1',
      invocationId: 'inv-1',
      step: 'start',
    });
  });

  it('propagates nested child bindings in onLog records', () => {
    const logger = new PinoLoggerAdapter({ level: 'debug' });
    const callback = vi.fn();
    logger.onLog(callback);

    const child = logger.child({ traceId: 'trace-1' }).child({ spanId: 'span-2' });
    child.debug('nested');

    expect(callback).toHaveBeenCalledTimes(1);
    const [record] = callback.mock.calls[0]!;
    expect(record.fields).toMatchObject({
      traceId: 'trace-1',
      spanId: 'span-2',
    });
  });

  it('waits for asynchronous logger extensions before resolving flush', async () => {
    const logger = new PinoLoggerAdapter({ level: 'fatal' });
    let beginDelivery: () => void = () => {};
    let finishDelivery: () => void = () => {};
    const started = new Promise<void>((resolve) => { beginDelivery = resolve; });
    const blocked = new Promise<void>((resolve) => { finishDelivery = resolve; });
    let completed = false;
    logger.onLog(async () => {
      beginDelivery();
      await blocked;
      completed = true;
    });

    logger.info('pending extension delivery');
    await started;
    let flushCompleted = false;
    const flushing = logger.flush().then(() => { flushCompleted = true; });
    await new Promise<void>((resolve) => setImmediate(resolve));
    expect(completed).toBe(false);
    expect(flushCompleted).toBe(false);

    finishDelivery();
    await flushing;
    expect(completed).toBe(true);
  });
});

