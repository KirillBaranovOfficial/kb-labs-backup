import { describe, expect, it } from 'vitest';
import type { BaseDocument } from './adapters/database.js';
import { createInMemoryDocumentDatabase } from './inmemory/adapters/document-database.js';
import { PLATFORM_AUDIT_COLLECTION, PlatformAuditStore } from './audit-store.js';

describe('PlatformAuditStore', () => {
  it('persists Gateway auth and MCP tool-call events in its shared collection', async () => {
    const docs = createInMemoryDocumentDatabase();
    const audit = new PlatformAuditStore(docs, () => 1234);

    await audit.record({
      action: 'user.password_reset_link_created',
      actorUserId: 'admin-1',
      targetUserId: 'member-1',
      tenantId: 'tenant-1',
    });
    await audit.record({
      kind: 'mcp.tool.call',
      actorUserId: 'user-1',
      tokenId: 'token-1',
      tenantId: 'tenant-1',
      project: '/projects/one',
      tool: 'fixture__mutate',
      outcome: 'success',
      correlationId: 'call-1',
    });

    const events = await docs.find<BaseDocument & Record<string, unknown>>(PLATFORM_AUDIT_COLLECTION, {});
    expect(events).toHaveLength(2);
    expect(events[0]).toMatchObject({
      action: 'user.password_reset_link_created', actorUserId: 'admin-1',
      targetUserId: 'member-1', tenantId: 'tenant-1', occurredAt: 1234,
    });
    expect(events[1]).toMatchObject({
      kind: 'mcp.tool.call', actorUserId: 'user-1', tokenId: 'token-1',
      tenantId: 'tenant-1', project: '/projects/one', tool: 'fixture__mutate',
      outcome: 'success', correlationId: 'call-1', occurredAt: 1234,
    });
  });
});
