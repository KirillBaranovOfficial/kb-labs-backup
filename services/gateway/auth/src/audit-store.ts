import { randomUUID } from 'node:crypto';
import type { BaseDocument, IDocumentDatabase } from '@kb-labs/core-platform/adapters';

const COLLECTION = 'auth_audit_events';

export interface AuthAuditEvent {
  eventId: string;
  action: string;
  actorUserId: string;
  targetUserId: string;
  tenantId: string;
  occurredAt: number;
  metadata?: Record<string, string>;
}

interface AuthAuditDoc extends BaseDocument, AuthAuditEvent {}

export class AuthAuditStore {
  private initialised: Promise<void> | null = null;
  constructor(private readonly docs: IDocumentDatabase, private readonly now: () => number = Date.now) {}

  async record(input: Omit<AuthAuditEvent, 'eventId' | 'occurredAt'>): Promise<void> {
    if (!this.initialised) {
      this.initialised = this.docs.ensureCollection(COLLECTION, {
        indexes: [{ path: 'eventId', unique: true }, { path: ['tenantId', 'occurredAt'] }, { path: 'targetUserId' }],
      });
    }
    await this.initialised;
    await this.docs.insertOne<AuthAuditDoc>(COLLECTION, {
      ...input,
      eventId: randomUUID(),
      occurredAt: this.now(),
    });
  }
}
