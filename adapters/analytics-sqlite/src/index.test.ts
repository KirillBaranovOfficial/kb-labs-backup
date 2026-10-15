import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import Database from 'better-sqlite3';
import { resolveRuntimeStatePath } from '@kb-labs/sdk/adapters';
import { toErrorEnvelope } from '@kb-labs/sdk';
import { SQLiteAnalytics } from './index.js';

let tmpDir: string;

function makeAdapter() {
  return new SQLiteAnalytics({
    dbPath: join(tmpDir, 'test.sqlite'),
    context: {
      source: { product: 'test', version: '1.0.0' },
      runId: 'run-test',
    },
  });
}

describe('SQLiteAnalytics', () => {
  let db: SQLiteAnalytics;

  beforeEach(() => {
    tmpDir = mkdtempSync(join(tmpdir(), 'kb-analytics-'));
    db = makeAdapter();
  });
  afterEach(() => {
    db.close();
    rmSync(tmpDir, { recursive: true, force: true });
  });

  // case: S-191.7
  it('maps a non-positive retentionDays value to KB_CONFIG_INVALID', () => {
    let failure: unknown;
    try {
      new SQLiteAnalytics({ dbPath: join(tmpDir, 'invalid.sqlite'), retentionDays: 0 });
    } catch (error) {
      failure = error;
    }
    expect(toErrorEnvelope(failure)).toMatchObject({
      code: 'KB_CONFIG_INVALID',
      message: 'Adapter option "platform.adapterOptions.analytics.retentionDays" is invalid: must be a positive integer.',
      hint: 'kb config set --scope platform platform.adapterOptions.analytics.retentionDays 90',
      details: { path: 'platform.adapterOptions.analytics.retentionDays' },
    });
  });

  // case: S-211.3
  it('logs scheduled pruning failures through the injected platform logger', async () => {
    vi.useFakeTimers();
    const databasePath = join(tmpDir, 'prune-failure.sqlite');
    const logger = {
      error: vi.fn(), info: vi.fn(), warn: vi.fn(), fatal: vi.fn(), debug: vi.fn(), trace: vi.fn(),
      child: vi.fn(),
      flush: vi.fn().mockResolvedValue(undefined),
    };
    const adapter = new SQLiteAnalytics({ dbPath: databasePath, logger });
    try {
      const rawDb = new Database(databasePath);
      rawDb.exec('DROP TABLE events');
      rawDb.close();
      await vi.advanceTimersByTimeAsync(24 * 60 * 60 * 1000);
      expect(logger.error).toHaveBeenCalledWith('Analytics retention pruning failed', expect.any(Error), expect.objectContaining({
        adapter: 'analytics-sqlite',
        operation: 'retention-prune',
      }));
    } finally {
      adapter.close();
      vi.useRealTimers();
    }
  });

  // ── track / getEvents ──────────────────────────────────────────────────────

  it('track stores event and getEvents returns it', async () => {
    await db.track('llm.complete', { model: 'gpt-4', totalTokens: 100 });
    const { events, total } = await db.getEvents();
    expect(total).toBe(1);
    expect(events[0]?.type).toBe('llm.complete');
    expect(events[0]?.payload).toEqual({ model: 'gpt-4', totalTokens: 100 });
  });

  // case: S-212.2
  it('prunes events older than the configured retention on startup', async () => {
    const databasePath = join(tmpDir, 'retention.sqlite');
    const seed = new SQLiteAnalytics({ dbPath: databasePath, retentionDays: 365 });
    await seed.track('retention.old');
    await seed.track('retention.recent');
    seed.close();

    const rawDb = new Database(databasePath);
    rawDb.prepare('UPDATE events SET ts = ? WHERE type = ?')
      .run(new Date(Date.now() - 40 * 24 * 60 * 60 * 1000).toISOString(), 'retention.old');
    rawDb.close();

    const restarted = new SQLiteAnalytics({ dbPath: databasePath, retentionDays: 30 });
    try {
      expect((await restarted.getEvents({ type: 'retention.old' })).events).toHaveLength(0);
      expect((await restarted.getEvents({ type: 'retention.recent' })).events).toHaveLength(1);
    } finally {
      restarted.close();
    }
  });

  // case: S-212.1
  it('uses a 90-day retention period when none is configured', async () => {
    const databasePath = join(tmpDir, 'default-retention.sqlite');
    const seed = new SQLiteAnalytics({ dbPath: databasePath, retentionDays: 365 });
    await seed.track('retention.default-old');
    seed.close();

    const rawDb = new Database(databasePath);
    rawDb.prepare('UPDATE events SET ts = ? WHERE type = ?')
      .run(new Date(Date.now() - 100 * 24 * 60 * 60 * 1000).toISOString(), 'retention.default-old');
    rawDb.close();

    const restarted = new SQLiteAnalytics({ dbPath: databasePath });
    try {
      expect((await restarted.getEvents({ type: 'retention.default-old' })).events).toHaveLength(0);
    } finally {
      restarted.close();
    }
  });

  // case: S-212.2
  it('prunes events at least daily while the adapter remains open', async () => {
    vi.useFakeTimers();
    const databasePath = join(tmpDir, 'periodic-retention.sqlite');
    const timersBeforeAdapter = vi.getTimerCount();
    const periodic = new SQLiteAnalytics({ dbPath: databasePath, retentionDays: 30 });
    try {
      expect(vi.getTimerCount()).toBe(timersBeforeAdapter + 1);
      await periodic.track('retention.periodic');
      const rawDb = new Database(databasePath);
      rawDb.prepare('UPDATE events SET ts = ? WHERE type = ?')
        .run(new Date(Date.now() - 31 * 24 * 60 * 60 * 1000).toISOString(), 'retention.periodic');
      rawDb.close();

      await vi.advanceTimersByTimeAsync(24 * 60 * 60 * 1000);

      expect((await periodic.getEvents({ type: 'retention.periodic' })).events).toHaveLength(0);
      periodic.dispose();
      expect(vi.getTimerCount()).toBe(timersBeforeAdapter);
    } finally {
      periodic.dispose();
      vi.useRealTimers();
    }
  });

  it('getEvents filters by type', async () => {
    await db.track('llm.complete');
    await db.track('embeddings.embed');
    const { events } = await db.getEvents({ type: 'llm.complete' });
    expect(events).toHaveLength(1);
    expect(events[0]?.type).toBe('llm.complete');
  });

  it('getEvents filters by source', async () => {
    await db.track('llm.complete');
    db.setSource({ product: 'other', version: '1.0.0' });
    await db.track('llm.complete');
    const { events, total } = await db.getEvents({ source: 'other' });
    expect(total).toBe(1);
    expect(events[0]?.source.product).toBe('other');
  });

  it('getEvents pagination works', async () => {
    for (let i = 0; i < 5; i++) { await db.track('evt'); }
    const page1 = await db.getEvents({ limit: 2, offset: 0 });
    const page2 = await db.getEvents({ limit: 2, offset: 2 });
    expect(page1.events).toHaveLength(2);
    expect(page2.events).toHaveLength(2);
    expect(page1.total).toBe(5);
    expect(page1.hasMore).toBe(true);
  });

  it('getEvents returns hasMore=false on last page', async () => {
    await db.track('evt');
    const { hasMore } = await db.getEvents({ limit: 10, offset: 0 });
    expect(hasMore).toBe(false);
  });

  it('identify tracks identity event', async () => {
    await db.identify('user-123', { plan: 'pro' });
    const { events } = await db.getEvents({ type: 'identity' });
    expect(events).toHaveLength(1);
    expect(events[0]?.payload).toEqual({ userId: 'user-123', plan: 'pro' });
  });

  // ── getStats ───────────────────────────────────────────────────────────────

  it('getStats returns zero counts for empty DB', async () => {
    const stats = await db.getStats();
    expect(stats.totalEvents).toBe(0);
    expect(stats.byType).toEqual({});
  });

  it('getStats counts byType correctly', async () => {
    await db.track('llm.complete');
    await db.track('llm.complete');
    await db.track('embeddings.embed');
    const stats = await db.getStats();
    expect(stats.totalEvents).toBe(3);
    expect(stats.byType['llm.complete']).toBe(2);
    expect(stats.byType['embeddings.embed']).toBe(1);
  });

  it('getStats bySource uses context product', async () => {
    await db.track('evt');
    const stats = await db.getStats();
    expect(stats.bySource['test']).toBe(1);
  });

  // ── getDailyStats ──────────────────────────────────────────────────────────

  it('getDailyStats groups events by day', async () => {
    await db.track('llm.complete', { totalTokens: 50 });
    await db.track('llm.complete', { totalTokens: 100 });
    const rows = await db.getDailyStats({ type: 'llm.complete', groupBy: 'day' });
    expect(rows).toHaveLength(1);
    expect(rows[0]?.metrics?.totalTokens).toBe(150);
  });

  it('getDailyStats respects groupBy=hour format', async () => {
    await db.track('evt');
    const rows = await db.getDailyStats({ groupBy: 'hour' });
    expect(rows).toHaveLength(1);
    // hour format: YYYY-MM-DDTHH:00
    expect(rows[0]?.date).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:00$/);
  });

  it('getDailyStats returns empty array for no matching events', async () => {
    await db.track('evt');
    const rows = await db.getDailyStats({ type: 'nonexistent' });
    expect(rows).toHaveLength(0);
  });

  it('getDailyStats breakdownBy splits by payload field', async () => {
    await db.track('llm.complete', { model: 'gpt-4', totalTokens: 100 });
    await db.track('llm.complete', { model: 'gpt-3.5', totalTokens: 50 });
    const rows = await db.getDailyStats({ type: 'llm.complete', breakdownBy: 'payload.model' });
    expect(rows.length).toBeGreaterThanOrEqual(2);
    const models = rows.map((r) => r.breakdown);
    expect(models).toContain('gpt-4');
    expect(models).toContain('gpt-3.5');
  });

  // ── source attribution ─────────────────────────────────────────────────────

  it('getSource returns context product/version', () => {
    expect(db.getSource()).toEqual({ product: 'test', version: '1.0.0' });
  });

  it('setSource updates context', () => {
    db.setSource({ product: 'updated', version: '2.0.0' });
    expect(db.getSource()).toEqual({ product: 'updated', version: '2.0.0' });
  });

  // ── lifecycle ──────────────────────────────────────────────────────────────

  it('flush resolves without error (synchronous adapter)', async () => {
    await expect(db.flush()).resolves.toBeUndefined();
  });

  it('close is idempotent', () => {
    db.close();
    expect(() => db.close()).not.toThrow();
  });

  it('dispose delegates to close', () => {
    expect(() => db.dispose()).not.toThrow();
  });

  // case: S-054.10
  it('keeps analytics events readable across a platform state restart cycle', async () => {
    const projectRoot = join(tmpDir, 'project');
    const kbHome = join(tmpDir, 'kb-home');
    const previousKbHome = process.env.KB_HOME;
    process.env.KB_HOME = kbHome;

    try {
      const firstStart = new SQLiteAnalytics({ workspace: { cwd: projectRoot } });
      const databasePath = resolveRuntimeStatePath(projectRoot, ['analytics', 'analytics.sqlite']);
      expect(firstStart.getSource()).toEqual({ product: 'unknown', version: '0.0.0' });
      await firstStart.track('journey.restart-check', { check: true });
      firstStart.close();

      const restarted = new SQLiteAnalytics({ workspace: { cwd: projectRoot } });
      const { events } = await restarted.getEvents({ type: 'journey.restart-check' });
      expect(events).toHaveLength(1);
      expect(events[0]?.payload).toEqual({ check: true });
      expect(databasePath).toContain(join(kbHome, 'state'));
      restarted.close();
    } finally {
      if (previousKbHome === undefined) delete process.env.KB_HOME;
      else process.env.KB_HOME = previousKbHome;
    }
  });

  it('reports a corrupt analytics file as a typed actionable adapter error', () => {
    const databasePath = join(tmpDir, 'corrupt.sqlite');
    writeFileSync(databasePath, 'not a sqlite database');

    let thrown: unknown;
    try {
      new SQLiteAnalytics({ dbPath: databasePath });
    } catch (error) {
      thrown = error;
    }

    const envelope = toErrorEnvelope(thrown);
    expect(envelope.code).toBe('KB_HOST_ADAPTER_UNAVAILABLE');
    expect(envelope.message).toContain(databasePath);
    expect(envelope.hint).toContain('kb-create stop --platform-root "$PLATFORM_ROOT"');
    expect(envelope.hint).toContain('kb-create start --platform-root "$PLATFORM_ROOT"');
  });
});
