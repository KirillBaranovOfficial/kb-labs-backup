import { createHash, randomBytes } from 'node:crypto';
import type { BaseDocument, IDocumentDatabase } from '@kb-labs/core-platform/adapters';

const COLLECTION = 'password_resets';

export interface PasswordResetRecord {
  resetId: string;
  userId: string;
  tenantId: string;
  createdBy: string;
  createdAt: number;
  expiresAt: number;
  status: 'active' | 'used' | 'revoked';
}

interface PasswordResetDoc extends BaseDocument, PasswordResetRecord {
  tokenHash: string;
}

const hashToken = (token: string): string => createHash('sha256').update(token).digest('hex');

export class PasswordResetStore {
  private initialised: Promise<void> | null = null;

  constructor(private readonly docs: IDocumentDatabase, private readonly now: () => number = Date.now) {}

  private async ensureSchema(): Promise<void> {
    if (!this.initialised) {
      this.initialised = this.docs.ensureCollection(COLLECTION, {
        indexes: [
          { path: 'resetId', unique: true },
          { path: 'tokenHash', unique: true },
          { path: ['tenantId', 'userId'] },
          { path: 'expiresAt', ttl: 0 },
        ],
      });
    }
    await this.initialised;
  }

  async create(input: { userId: string; tenantId: string; createdBy: string; ttlMs: number }): Promise<{ resetId: string; token: string; expiresAt: number }> {
    await this.ensureSchema();
    const token = randomBytes(32).toString('base64url');
    const resetId = randomBytes(16).toString('base64url');
    const createdAt = this.now();
    const expiresAt = createdAt + input.ttlMs;
    await this.docs.insertOne<PasswordResetDoc>(COLLECTION, {
      resetId, userId: input.userId, tenantId: input.tenantId, createdBy: input.createdBy,
      expiresAt, status: 'active', tokenHash: hashToken(token),
    });
    return { resetId, token, expiresAt };
  }

  async consume(token: string): Promise<PasswordResetRecord | null> {
    await this.ensureSchema();
    return this.docs.transaction(async (tx) => {
      const [record] = await tx.find<PasswordResetDoc>(COLLECTION, { tokenHash: { $eq: hashToken(token) } }, { limit: 1 });
      if (!record || record.status !== 'active' || record.expiresAt <= this.now()) {return null;}
      const updated = await tx.updateMany<PasswordResetDoc>(COLLECTION,
        { $and: [{ resetId: { $eq: record.resetId } }, { status: { $eq: 'active' } }, { expiresAt: { $gt: this.now() } }] },
        { $set: { status: 'used' } });
      if (updated === 0) {return null;}
      const { tokenHash: _tokenHash, ...safeRecord } = record;
      return { ...safeRecord, status: 'used' };
    });
  }
}
