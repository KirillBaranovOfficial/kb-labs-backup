/**
 * @module @kb-labs/core-platform/noop/adapters/__tests__/throwers
 *
 * Behavioural tests for the NoOp adapter throwers — they should throw
 * `AdapterUnavailableError` from every functional call, with the slot
 * name attached for clean error reporting.
 */

import { describe, it, expect, vi } from 'vitest';
import { AdapterUnavailableError } from '../../../errors.js';
import { NoOpLLM } from '../llm.js';
import { NoOpEmbeddings } from '../embeddings.js';
import { UnavailableNotifier } from '../notifier.js';
import { UnavailableLogReader } from '../log-reader.js';
import { UnavailableVectorStore } from '../vector-store.js';
import { inmemoryFactories } from '../../../inmemory/index.js';
import { InMemoryNotifier } from '../../../inmemory/adapters/notifier.js';
import { noopFactories } from '../../index.js';

describe('NoOpLLM', () => {
  const llm = new NoOpLLM();

  it('complete() throws AdapterUnavailableError with slot=llm', async () => {
    await expect(llm.complete('hi')).rejects.toBeInstanceOf(AdapterUnavailableError);
    try {
      await llm.complete('hi');
    } catch (err) {
      expect((err as AdapterUnavailableError).slot).toBe('llm');
      expect((err as AdapterUnavailableError).reason).toBe('not-configured');
    }
  });

  it('stream() throws on iteration', async () => {
    const iter = llm.stream('hi');
    let caught: unknown;
    try {
      for await (const _ of iter) { void _; }
    } catch (e) {
      caught = e;
    }
    expect(caught).toBeInstanceOf(AdapterUnavailableError);
  });

  it('chatWithTools() throws AdapterUnavailableError', async () => {
    await expect(llm.chatWithTools!([], { tools: [] })).rejects.toBeInstanceOf(
      AdapterUnavailableError,
    );
  });
});

describe('NoOpEmbeddings', () => {
  const embed = new NoOpEmbeddings();

  it('embed() throws AdapterUnavailableError with slot=embeddings', async () => {
    try {
      await embed.embed('hello');
    } catch (err) {
      expect(err).toBeInstanceOf(AdapterUnavailableError);
      expect((err as AdapterUnavailableError).slot).toBe('embeddings');
    }
  });

  it('embedBatch() throws', async () => {
    await expect(embed.embedBatch(['a', 'b'])).rejects.toBeInstanceOf(
      AdapterUnavailableError,
    );
  });

  it('getDimensions() throws', async () => {
    await expect(embed.getDimensions()).rejects.toBeInstanceOf(AdapterUnavailableError);
  });
});

describe('UnavailableNotifier', () => {
  const notifier = new UnavailableNotifier();

  it('notify() throws a typed error instead of silently dropping the event', async () => {
    await expect(
      notifier.notify({
        severity: 'info',
        code: 'test.event',
        title: 'hi',
        body: 'msg',
      }),
    ).rejects.toMatchObject({ name: 'AdapterUnavailableError', slot: 'notifier' });
  });

  it('subscribe() throws a typed error instead of creating a dead subscription', () => {
    expect(() => notifier.subscribe({}, async () => {})).toThrow(AdapterUnavailableError);
  });
});

describe('UnavailableLogReader', () => {
  const reader = new UnavailableLogReader();

  it('query() throws a typed error when no local log reader is configured', async () => {
    await expect(reader.query({})).rejects.toMatchObject({ name: 'AdapterUnavailableError', slot: 'logs' });
  });

  it('getById() throws when no local reader exists', async () => {
    await expect(reader.getById('any-id')).rejects.toMatchObject({ slot: 'logs' });
  });

  it('getCapabilities() reports no backends', () => {
    const caps = reader.getCapabilities();
    expect(caps.hasBuffer).toBe(false);
    expect(caps.hasPersistence).toBe(false);
    expect(caps.hasStreaming).toBe(false);
    expect(caps.hasSearch).toBe(false);
  });
});

describe('InMemoryNotifier', () => {
  it('delivers matching local notifications to subscribers', async () => {
    const notifier = new InMemoryNotifier();
    const handler = vi.fn(async () => {});
    notifier.subscribe({ severity: ['warn'] }, handler);
    await notifier.notify({ severity: 'warn', title: 'warning', body: 'local' });
    expect(handler).toHaveBeenCalledWith(expect.objectContaining({
      title: 'warning',
      id: expect.any(String),
      emittedAt: expect.any(Number),
    }));
    expect(inmemoryFactories.notifier()).toBeInstanceOf(InMemoryNotifier);
  });
});

describe('noopFactories', () => {
  it('provides factories for slots whose defaultFallback is noop', () => {
    expect(noopFactories.llm).toBeDefined();
    expect(noopFactories.embeddings).toBeDefined();
    expect(noopFactories.logs).toBeDefined();
    expect(noopFactories.vectorStore).toBeDefined();
    expect(Object.hasOwn(noopFactories, 'notifier')).toBe(false);
    expect(inmemoryFactories.notifier).toBeDefined();
  });

  it('factory returns a fresh instance each call', () => {
    const a = noopFactories.llm();
    const b = noopFactories.llm();
    expect(a).toBeInstanceOf(NoOpLLM);
    expect(b).toBeInstanceOf(NoOpLLM);
    expect(a).not.toBe(b);
  });
});

describe('UnavailableVectorStore', () => {
  const store = new UnavailableVectorStore();

  it('throws a typed missing-capability error instead of returning empty results', async () => {
    await expect(store.search([], 1)).rejects.toMatchObject({
      name: 'AdapterUnavailableError',
      slot: 'vectorStore',
      reason: 'not-configured',
    });
    await expect(store.count()).rejects.toMatchObject({ slot: 'vectorStore' });
  });
});
