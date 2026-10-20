import type { BaseDocument, IDocumentDatabase } from '@kb-labs/core-platform/adapters';
import {
  WebhookHistoryRecordSchema,
  WEBHOOK_HISTORY_RETENTION_LIMIT,
  type WebhookHistoryRecord,
} from '@kb-labs/gateway-contracts';

const COLLECTION = 'gateway_webhook_history';

type WebhookHistoryDocument = BaseDocument & WebhookHistoryRecord;

export class WebhookHistoryStore {
  private initialised: Promise<void> | undefined;
  private readonly pruneTails = new Map<string, Promise<void>>();

  constructor(
    private readonly documents: IDocumentDatabase,
    private readonly retentionLimit = WEBHOOK_HISTORY_RETENTION_LIMIT,
  ) {}

  async record(input: WebhookHistoryRecord): Promise<void> {
    const record = WebhookHistoryRecordSchema.parse(input);
    await this.ensureCollection();
    await this.documents.insertOne<WebhookHistoryDocument>(COLLECTION, record);
    await this.prune(record);
  }

  async get(project: string, webhookId: string): Promise<WebhookHistoryRecord | null> {
    await this.ensureCollection();
    const [document] = await this.documents.find<WebhookHistoryDocument>(
      COLLECTION,
      { project, webhookId },
      { limit: 1 },
    );
    return document ? this.toRecord(document) : null;
  }

  async listEndpoint(
    project: string,
    pluginId: string,
    event: string,
    instanceId: string | null,
  ): Promise<WebhookHistoryRecord[]> {
    await this.ensureCollection();
    const documents = await this.documents.find<WebhookHistoryDocument>(
      COLLECTION,
      { project, pluginId, event, instanceId },
      { sort: { receivedAt: -1, webhookId: -1 }, limit: this.retentionLimit },
    );
    return documents.map((document) => this.toRecord(document));
  }

  private async ensureCollection(): Promise<void> {
    this.initialised ??= this.documents.ensureCollection(COLLECTION, {
      indexes: [
        { path: 'webhookId', unique: true },
        { path: ['project', 'pluginId', 'event', 'instanceId', 'receivedAt'] },
      ],
    });
    await this.initialised;
  }

  private async prune(record: WebhookHistoryRecord): Promise<void> {
    const key = JSON.stringify([record.project, record.pluginId, record.event, record.instanceId]);
    const previous = this.pruneTails.get(key);
    let release!: () => void;
    const current = new Promise<void>((resolve) => { release = resolve; });
    this.pruneTails.set(key, current);
    await previous;
    try {
      const stale = await this.documents.find<WebhookHistoryDocument>(
        COLLECTION,
        { project: record.project, pluginId: record.pluginId, event: record.event, instanceId: record.instanceId },
        { sort: { receivedAt: -1, webhookId: -1 }, skip: this.retentionLimit },
      );
      if (stale.length > 0) {
        await this.documents.deleteMany<WebhookHistoryDocument>(COLLECTION, {
          id: { $in: stale.map((document) => document.id) },
        });
      }
    } finally {
      if (this.pruneTails.get(key) === current) {
        this.pruneTails.delete(key);
      }
      release();
    }
  }

  private toRecord(document: WebhookHistoryDocument): WebhookHistoryRecord {
    const { id: _id, createdAt: _createdAt, updatedAt: _updatedAt, ...record } = document;
    return WebhookHistoryRecordSchema.parse(record);
  }
}
