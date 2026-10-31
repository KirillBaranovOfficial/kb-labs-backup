import { open, mkdir, readFile, rename } from 'node:fs/promises';
import path from 'node:path';
import { InMemoryStateBroker, type StateBrokerSnapshotEntry } from './in-memory.js';

type JournalRecord =
  | { version: 1; operation: 'set'; key: string; value: unknown; expiresAt: number }
  | { version: 1; operation: 'delete'; key: string }
  | { version: 1; operation: 'clear'; pattern?: string };

const DEFAULT_TTL_MS = 300_000;
const COMPACT_AFTER_RECORDS = 1_000;

/** StateBroker backend that syncs every resolved mutation to a local journal. */
export class AppendOnlyJournalStateBroker extends InMemoryStateBroker {
  private readonly journalPath: string;
  private writeQueue: Promise<void> = Promise.resolve();
  private journalRecords = 0;
  private corrupted = false;

  private constructor(dataRoot: string, cleanupIntervalMs: number) {
    super(cleanupIntervalMs);
    this.journalPath = path.join(dataRoot, 'state-broker', 'journal.jsonl');
  }

  static async open(dataRoot: string, cleanupIntervalMs = 30_000): Promise<AppendOnlyJournalStateBroker> {
    const broker = new AppendOnlyJournalStateBroker(dataRoot, cleanupIntervalMs);
    await mkdir(path.dirname(broker.journalPath), { recursive: true });
    await broker.replay();
    return broker;
  }

  get journalWasCorrupt(): boolean {
    return this.corrupted;
  }

  override async set<T>(key: string, value: T, ttl = DEFAULT_TTL_MS): Promise<void> {
    const expiresAt = Date.now() + ttl;
    const record: JournalRecord = { version: 1, operation: 'set', key, value, expiresAt };
    await this.enqueue(async () => {
      await this.append(record);
      await super.set(key, value, Math.max(0, expiresAt - Date.now()));
      await this.compactIfNeeded();
    });
  }

  override async delete(key: string): Promise<void> {
    await this.enqueue(async () => {
      await this.append({ version: 1, operation: 'delete', key });
      await super.delete(key);
      await this.compactIfNeeded();
    });
  }

  override async clear(pattern?: string): Promise<void> {
    await this.enqueue(async () => {
      await this.append(pattern === undefined
        ? { version: 1, operation: 'clear' }
        : { version: 1, operation: 'clear', pattern });
      await super.clear(pattern);
      await this.compactIfNeeded();
    });
  }

  override async setIfNotExists<T>(key: string, value: T, ttl = DEFAULT_TTL_MS): Promise<boolean> {
    return this.enqueue(async () => {
      if (this.hasLiveEntry(key)) return false;
      const expiresAt = Date.now() + ttl;
      await this.append({ version: 1, operation: 'set', key, value, expiresAt });
      await super.set(key, value, Math.max(0, expiresAt - Date.now()));
      await this.compactIfNeeded();
      return true;
    });
  }

  override async stop(): Promise<void> {
    try {
      await this.writeQueue;
    } finally {
      await super.stop();
    }
  }

  private enqueue<T>(operation: () => Promise<T>): Promise<T> {
    const result = this.writeQueue.then(operation);
    this.writeQueue = result.then(() => undefined);
    return result;
  }

  private async append(record: JournalRecord): Promise<void> {
    const handle = await open(this.journalPath, 'a');
    try {
      await handle.writeFile(`${JSON.stringify(record)}\n`, 'utf8');
      await handle.sync();
    } finally {
      await handle.close();
    }
    this.journalRecords++;
  }

  private async replay(): Promise<void> {
    let contents: string;
    try {
      contents = await readFile(this.journalPath, 'utf8');
    } catch (error) {
      if (isMissingPath(error)) return;
      throw error;
    }

    let verifiedBytes = 0;
    const lines = contents.split('\n');
    for (let index = 0; index < lines.length; index++) {
      const line = lines[index]!;
      if (index === lines.length - 1 && line.length === 0) break;
      const completeLine = index < lines.length - 1;
      let record: JournalRecord;
      try {
        if (!completeLine) throw new Error('incomplete final record');
        const parsed: unknown = JSON.parse(line);
        if (!isJournalRecord(parsed)) throw new Error('record does not match journal format');
        record = parsed;
      } catch {
        this.corrupted = true;
        break;
      }
      await this.apply(record);
      verifiedBytes += Buffer.byteLength(line, 'utf8') + 1;
      this.journalRecords++;
    }

    if (this.corrupted) {
      const handle = await open(this.journalPath, 'r+');
      try {
        await handle.truncate(verifiedBytes);
        await handle.sync();
      } finally {
        await handle.close();
      }
    }
  }

  private async apply(record: JournalRecord): Promise<void> {
    switch (record.operation) {
      case 'set': {
        const ttl = record.expiresAt - Date.now();
        if (ttl > 0) await super.set(record.key, record.value, ttl);
        return;
      }
      case 'delete':
        await super.delete(record.key);
        return;
      case 'clear':
        await super.clear(record.pattern);
        return;
    }
  }

  private async compactIfNeeded(): Promise<void> {
    if (this.journalRecords < COMPACT_AFTER_RECORDS) return;
    const entries: StateBrokerSnapshotEntry[] = this.snapshotEntries();
    const temporaryPath = `${this.journalPath}.${process.pid}.${Date.now()}.tmp`;
    const handle = await open(temporaryPath, 'w');
    try {
      for (const entry of entries) {
        const record: JournalRecord = {
          version: 1,
          operation: 'set',
          key: entry.key,
          value: entry.value,
          expiresAt: entry.expiresAt,
        };
        await handle.writeFile(`${JSON.stringify(record)}\n`, 'utf8');
      }
      await handle.sync();
    } finally {
      await handle.close();
    }
    await rename(temporaryPath, this.journalPath);
    const directory = await open(path.dirname(this.journalPath), 'r');
    try {
      await directory.sync();
    } finally {
      await directory.close();
    }
    this.journalRecords = entries.length;
  }
}

function isJournalRecord(value: unknown): value is JournalRecord {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return false;
  const record = value as Record<string, unknown>;
  if (record.version !== 1 || typeof record.operation !== 'string') return false;
  if (record.operation === 'set') {
    return typeof record.key === 'string' && typeof record.expiresAt === 'number' && Number.isFinite(record.expiresAt) && 'value' in record;
  }
  if (record.operation === 'delete') return typeof record.key === 'string';
  if (record.operation === 'clear') return record.pattern === undefined || typeof record.pattern === 'string';
  return false;
}

function isMissingPath(error: unknown): boolean {
  return error instanceof Error && 'code' in error && error.code === 'ENOENT';
}
