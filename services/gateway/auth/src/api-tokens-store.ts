import { createHash, randomBytes, randomUUID } from 'node:crypto';
import type { BaseDocument, IDocumentDatabase } from '@kb-labs/core-platform/adapters';

const COLLECTION = 'user_api_tokens';
const TOKEN_PREFIX = 'kbl_pat_';

export interface ApiTokenRecord {
  tokenId: string;
  userId: string;
  tenantId: string;
  name: string;
  issuedAt: number;
  expiresAt: number;
  revokedAt?: number;
}

interface ApiTokenDoc extends BaseDocument, ApiTokenRecord {
  tokenHash: string;
}

const hash = (token: string): string => createHash('sha256').update(token).digest('hex');

export class ApiTokensStore {
  private initialised: Promise<void> | null = null;

  constructor(private readonly docs: IDocumentDatabase, private readonly now: () => number = Date.now) {}

  private async ensureSchema(): Promise<void> {
    if (!this.initialised) {
      this.initialised = this.docs.ensureCollection(COLLECTION, {
        indexes: [
          { path: 'tokenId', unique: true },
          { path: 'tokenHash', unique: true },
          { path: 'userId' },
          { path: 'expiresAt', ttl: 0 },
        ],
      });
    }
    await this.initialised;
  }

  async create(input: { userId: string; tenantId: string; name: string; ttlMs: number }): Promise<{ record: ApiTokenRecord; token: string }> {
    await this.ensureSchema();
    const now = this.now();
    const token = `${TOKEN_PREFIX}${randomBytes(32).toString('base64url')}`;
    const record: ApiTokenRecord = {
      tokenId: randomUUID(),
      userId: input.userId,
      tenantId: input.tenantId,
      name: input.name,
      issuedAt: now,
      expiresAt: now + input.ttlMs,
    };
    await this.docs.insertOne<ApiTokenDoc>(COLLECTION, { ...record, tokenHash: hash(token) });
    return { record, token };
  }

  async findActive(token: string): Promise<ApiTokenRecord | null> {
    if (!token.startsWith(TOKEN_PREFIX)) return null;
    await this.ensureSchema();
    const [record] = await this.docs.find<ApiTokenDoc>(COLLECTION, { tokenHash: { $eq: hash(token) } }, { limit: 1 });
    if (!record || record.revokedAt !== undefined || record.expiresAt <= this.now()) return null;
    const { tokenHash: _hash, ...publicRecord } = record;
    return publicRecord;
  }

  async listByUser(userId: string): Promise<ApiTokenRecord[]> {
    await this.ensureSchema();
    const records = await this.docs.find<ApiTokenDoc>(COLLECTION, { userId: { $eq: userId } }, { sort: { issuedAt: -1 } });
    return records.map(({ tokenHash: _hash, ...record }) => record);
  }

  async revokeByUser(tokenId: string, userId: string): Promise<boolean> {
    await this.ensureSchema();
    const result = await this.docs.updateMany<ApiTokenDoc>(COLLECTION,
      { $and: [{ tokenId: { $eq: tokenId } }, { userId: { $eq: userId } }, { revokedAt: { $exists: false } }] },
      { $set: { revokedAt: this.now() } });
    return result > 0;
  }
}
