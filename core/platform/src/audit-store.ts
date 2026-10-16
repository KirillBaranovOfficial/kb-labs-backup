import { randomUUID } from 'node:crypto';
import type { BaseDocument, IDocumentDatabase } from './adapters/database.js';
import type { PlatformErrorCode } from './error-envelope/types.js';

export const PLATFORM_AUDIT_COLLECTION = 'auth_audit_events';

export interface AuthAuditInput {
  action: string;
  actorUserId: string;
  targetUserId: string;
  tenantId: string;
  metadata?: Record<string, string>;
}

export interface McpToolCallAuditInput {
  kind: 'mcp.tool.call';
  actorUserId: string | null;
  tokenId: string | null;
  tenantId: string;
  project: string;
  tool: string;
  outcome: 'success' | PlatformErrorCode;
  correlationId: string;
}

export interface PlatformAuditEvent {
  eventId: string;
  occurredAt: number;
}

type PlatformAuditDoc = BaseDocument & (AuthAuditInput | McpToolCallAuditInput) & PlatformAuditEvent;

export class PlatformAuditStore {
  private initialised: Promise<void> | null = null;

  constructor(private readonly docs: IDocumentDatabase, private readonly now: () => number = Date.now) {}

  async record(input: AuthAuditInput | McpToolCallAuditInput): Promise<void> {
    if (!this.initialised) {
      this.initialised = this.docs.ensureCollection(PLATFORM_AUDIT_COLLECTION, {
        indexes: [
          { path: 'eventId', unique: true },
          { path: ['tenantId', 'occurredAt'] },
          { path: 'targetUserId' },
          { path: 'tokenId' },
          { path: 'correlationId' },
        ],
      });
    }
    await this.initialised;
    await this.docs.insertOne<PlatformAuditDoc>(PLATFORM_AUDIT_COLLECTION, {
      ...input,
      eventId: randomUUID(),
      occurredAt: this.now(),
    });
  }
}
