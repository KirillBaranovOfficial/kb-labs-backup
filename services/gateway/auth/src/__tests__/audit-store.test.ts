import { describe, expect, it } from 'vitest';
import { createInMemoryDocumentDatabase } from '@kb-labs/sdk/testing';
import { AuthAuditStore, type AuthAuditEvent } from '../audit-store.js';

describe('AuthAuditStore', () => {
  it('persists the actor, target, tenant, and time for a reset-link event', async () => {
    const docs = createInMemoryDocumentDatabase();
    const audit = new AuthAuditStore(docs, () => 1234);
    await audit.record({
      action: 'user.password_reset_link_created',
      actorUserId: 'admin-1',
      targetUserId: 'member-1',
      tenantId: 'tenant-1',
    });
    const events = await docs.find<AuthAuditEvent>('auth_audit_events', {});
    expect(events).toHaveLength(1);
    expect(events[0]).toMatchObject({
      action: 'user.password_reset_link_created', actorUserId: 'admin-1',
      targetUserId: 'member-1', tenantId: 'tenant-1', occurredAt: 1234,
    });
  });
});
