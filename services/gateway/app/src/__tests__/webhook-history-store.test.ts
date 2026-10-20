import { beforeEach, describe, expect, it } from 'vitest';
import { createInMemoryDocumentDatabase } from '@kb-labs/sdk/testing';
import { WEBHOOK_HISTORY_RETENTION_LIMIT, type WebhookHistoryRecord } from '@kb-labs/gateway-contracts';
import { WebhookHistoryStore } from '../webhook/history-store.js';

function record(index: number, overrides: Partial<WebhookHistoryRecord> = {}): WebhookHistoryRecord {
  return {
    webhookId: `delivery-${index}`,
    project: 'project-a',
    receivedAt: new Date(1_800_000_000_000 + index).toISOString(),
    pluginId: 'fixture-plugin',
    event: 'alert',
    handler: './handler.js#default',
    instanceId: null,
    outcome: { type: 'delivered' },
    durationMs: index,
    correlationId: `request-${index}`,
    ...overrides,
  };
}

describe('WebhookHistoryStore', () => {
  let store: WebhookHistoryStore;
  let documents: ReturnType<typeof createInMemoryDocumentDatabase>;

  beforeEach(() => {
    documents = createInMemoryDocumentDatabase();
    store = new WebhookHistoryStore(documents, 2);
  });

  it('validates and persists one schema record without payload or secret fields', async () => {
    await store.record(record(1));
    const found = await store.get('project-a', 'delivery-1');
    expect(found).toEqual(record(1));
    const withPayload = { ...record(2), payload: { private: true } };
    await expect(store.record(withPayload)).rejects.toThrow();
  });

  it('returns history only for the requested project', async () => {
    await store.record(record(1));
    expect(await store.get('project-b', 'delivery-1')).toBeNull();
  });

  it('prunes the oldest records per project, plugin, event, and instance', async () => {
    await store.record(record(1));
    await store.record(record(2));
    await store.record(record(3));
    expect(await store.listEndpoint('project-a', 'fixture-plugin', 'alert', null)).toEqual([record(3), record(2)]);
    expect(await documents.count('gateway_webhook_history', {})).toBe(2);
  });

  // case: S-250.2
  it('prunes independently per webhook endpoint when one handler serves multiple events and instances', async () => {
    const endpoint = (index: number, event: string, instanceId: string | null) => record(index, {
      handler: './shared-handler.js#default',
      event,
      instanceId,
    });
    for (const entry of [
      endpoint(1, 'alert', 'quiet'), endpoint(2, 'alert', 'quiet'), endpoint(3, 'alert', 'quiet'),
      endpoint(4, 'alert', 'customer-a'), endpoint(5, 'alert', 'customer-a'), endpoint(6, 'alert', 'customer-a'),
      endpoint(7, 'deploy', null), endpoint(8, 'deploy', null), endpoint(9, 'deploy', null),
    ]) {
      await store.record(entry);
    }
    for (const entry of [
      endpoint(10, 'alert', null), endpoint(11, 'alert', null), endpoint(12, 'alert', null),
    ]) {
      await store.record(entry);
    }

    expect((await store.listEndpoint('project-a', 'fixture-plugin', 'alert', null)).map(({ webhookId }) => webhookId))
      .toEqual(['delivery-12', 'delivery-11']);
    expect((await store.listEndpoint('project-a', 'fixture-plugin', 'alert', 'quiet')).map(({ webhookId }) => webhookId))
      .toEqual(['delivery-3', 'delivery-2']);
    expect((await store.listEndpoint('project-a', 'fixture-plugin', 'alert', 'customer-a')).map(({ webhookId }) => webhookId))
      .toEqual(['delivery-6', 'delivery-5']);
    expect((await store.listEndpoint('project-a', 'fixture-plugin', 'deploy', null)).map(({ webhookId }) => webhookId))
      .toEqual(['delivery-9', 'delivery-8']);
    expect(await documents.count('gateway_webhook_history', {})).toBe(8);
  });

  it('prunes endpoint history to the declared default of 100 records', async () => {
    store = new WebhookHistoryStore(documents);
    for (let index = 1; index <= WEBHOOK_HISTORY_RETENTION_LIMIT + 1; index += 1) {
      await store.record(record(index));
    }
    expect(await documents.count('gateway_webhook_history', {})).toBe(WEBHOOK_HISTORY_RETENTION_LIMIT);
    expect((await store.get('project-a', 'delivery-1'))).toBeNull();
  });

  it('declares the default retention limit as 100', () => {
    expect(WEBHOOK_HISTORY_RETENTION_LIMIT).toBe(100);
  });
});
