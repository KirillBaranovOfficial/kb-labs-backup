#!/usr/bin/env node
/**
 * Migration script: Import JSONL analytics events into DuckDB.
 *
 * Uses DuckDB's native read_json_auto() to load NDJSON files directly via SQL.
 * Much faster than parsing in JS — all I/O and transformation happens in DuckDB.
 *
 * Usage:
 *   node scripts/migrate-from-jsonl.mjs [--source <dir>] [--db <path>] [--dry-run]
 *
 * Defaults:
 *   --source  analytics/buffer
 *   --db      analytics/analytics.duckdb
 *
 * Example:
 *   node scripts/migrate-from-jsonl.mjs \
 *     --source analytics/buffer \
 *     --db analytics/analytics.duckdb
 */

import { DuckDBInstance } from '@duckdb/node-api';
import { existsSync, readdirSync, mkdirSync } from 'node:fs';
import { resolve, join, dirname } from 'node:path';
import { parseArgs } from 'node:util';
import { resolvePlatformDataRoot } from '@kb-labs/sdk/adapters';

// ─── Parse CLI args ───────────────────────────────────────────────────────────

const { values: args } = parseArgs({
  options: {
    'platform-root': { type: 'string' },
    source: { type: 'string', default: 'analytics/buffer' },
    db: { type: 'string', default: 'analytics/analytics.duckdb' },
    'dry-run': { type: 'boolean', default: false },
    help: { type: 'boolean', default: false },
  },
  strict: false,
});

if (args.help) {
  console.log(`
migrate-from-jsonl.mjs — Import JSONL events into DuckDB

Usage:
  node scripts/migrate-from-jsonl.mjs --platform-root <path> [options]

Options:
  --platform-root <path> Platform root that owns the data root (or set KB_PLATFORM_ROOT)
  --source <dir>    Source directory with .jsonl files (default: analytics/buffer)
  --db <path>       DuckDB database file path (default: analytics/analytics.duckdb)
  --dry-run         Show what would be migrated without writing
  --help            Show this help
`);
  process.exit(0);
}

const platformRoot = args['platform-root'] ?? process.env.KB_PLATFORM_ROOT;
if (!platformRoot) {
  console.error('Platform root is required. Pass --platform-root <path> or set KB_PLATFORM_ROOT, then rerun the migration.');
  process.exit(1);
}
const dataRoot = await resolvePlatformDataRoot(resolve(platformRoot));
const sourceDir = resolve(dataRoot, args.source);
const dbPath = resolve(dataRoot, args.db);
const isDryRun = args['dry-run'];

// ─── Validate source directory ────────────────────────────────────────────────

if (!existsSync(sourceDir)) {
  console.error(`❌ Source directory not found: ${sourceDir}`);
  process.exit(1);
}

const jsonlFiles = readdirSync(sourceDir)
  .filter((f) => f.endsWith('.jsonl'))
  .map((f) => join(sourceDir, f));

if (jsonlFiles.length === 0) {
  console.log(`⚠️  No .jsonl files found in: ${sourceDir}`);
  process.exit(0);
}

console.log(`\n📂 Source: ${sourceDir}`);
console.log(`📦 Database: ${dbPath}`);
console.log(`📄 JSONL files found: ${jsonlFiles.length}`);
if (isDryRun) console.log(`🔍 DRY RUN — no data will be written\n`);

// ─── Ensure DB directory exists ───────────────────────────────────────────────

if (!isDryRun) {
  const dbDir = dirname(dbPath);
  if (!existsSync(dbDir)) {
    mkdirSync(dbDir, { recursive: true });
    console.log(`📁 Created directory: ${dbDir}`);
  }
}

// ─── DuckDB setup ─────────────────────────────────────────────────────────────

const CREATE_TABLE = `
CREATE TABLE IF NOT EXISTS events (
  id          VARCHAR PRIMARY KEY,
  schema      VARCHAR NOT NULL DEFAULT 'kb.v1',
  type        VARCHAR NOT NULL,
  ts          TIMESTAMPTZ NOT NULL,
  ingest_ts   TIMESTAMPTZ,
  run_id      VARCHAR,
  product     VARCHAR,
  version     VARCHAR,
  actor_type  VARCHAR,
  actor_id    VARCHAR,
  actor_name  VARCHAR,
  ctx         JSON,
  payload     JSON
)
`;

const CREATE_INDEXES = [
  `CREATE INDEX IF NOT EXISTS events_ts_idx ON events (ts)`,
  `CREATE INDEX IF NOT EXISTS events_type_idx ON events (type)`,
  `CREATE INDEX IF NOT EXISTS events_product_idx ON events (product)`,
  `CREATE INDEX IF NOT EXISTS events_type_ts_idx ON events (type, ts)`,
];

// ─── Migration ────────────────────────────────────────────────────────────────

async function migrate() {
  // Build glob pattern for all jsonl files (DuckDB supports glob natively)
  const globPattern = join(sourceDir, '*.jsonl').replace(/\\/g, '/');

  if (isDryRun) {
    console.log(`\nWould load files matching: ${globPattern}`);
    console.log('\nWould run:');
    console.log(`  CREATE TABLE IF NOT EXISTS events (...)`);
    console.log(`  INSERT OR IGNORE INTO events SELECT ... FROM read_json_auto('${globPattern}', ...)`);

    // Still open DuckDB to count how many rows we'd insert (in-memory)
    const db = await DuckDBInstance.create(':memory:');
    const conn = await db.connect();
    try {
      const result = await conn.run(`
        SELECT COUNT(*) as total
        FROM read_json_auto('${globPattern}', format='newline_delimited', ignore_errors=true)
        WHERE id IS NOT NULL AND type IS NOT NULL AND ts IS NOT NULL
      `);
      const rows = await fetchAllRows(result);
      console.log(`\n📊 Events eligible for migration: ${rows[0]?.total ?? 0}`);
    } finally {
      conn.closeSync();
    }
    return;
  }

  // Open the real DuckDB file
  const db = await DuckDBInstance.create(dbPath);
  const conn = await db.connect();

  try {
    // Setup schema
    console.log('\n🔧 Setting up schema...');
    await conn.run(CREATE_TABLE);
    for (const idx of CREATE_INDEXES) {
      await conn.run(idx);
    }

    // Count before
    const beforeResult = await conn.run(`SELECT COUNT(*) as cnt FROM events`);
    const beforeRows = await fetchAllRows(beforeResult);
    const countBefore = Number(beforeRows[0]?.cnt ?? 0);
    console.log(`📊 Events in DB before migration: ${countBefore}`);

    // Count source rows
    const sourceCountResult = await conn.run(`
      SELECT COUNT(*) as total
      FROM read_json_auto('${globPattern}', format='newline_delimited', ignore_errors=true)
      WHERE id IS NOT NULL AND type IS NOT NULL AND ts IS NOT NULL
    `);
    const sourceRows = await fetchAllRows(sourceCountResult);
    const totalSource = Number(sourceRows[0]?.total ?? 0);
    console.log(`📄 Events in JSONL source: ${totalSource}`);

    if (totalSource === 0) {
      console.log('⚠️  No valid events found in source files.');
      return;
    }

    // Run migration INSERT
    console.log('\n⏳ Migrating...');
    const startMs = Date.now();

    await conn.run(`
      INSERT OR IGNORE INTO events
      SELECT
        CAST(id AS VARCHAR)                                    AS id,
        COALESCE(CAST(schema AS VARCHAR), 'kb.v1')            AS schema,
        CAST(type AS VARCHAR)                                  AS type,
        CAST(ts AS TIMESTAMPTZ)                               AS ts,
        TRY_CAST(ingestTs AS TIMESTAMPTZ)                     AS ingest_ts,
        CAST(runId AS VARCHAR)                                 AS run_id,
        CAST(source->>'product' AS VARCHAR)                   AS product,
        CAST(source->>'version' AS VARCHAR)                   AS version,
        CAST(actor->>'type' AS VARCHAR)                       AS actor_type,
        CAST(actor->>'id' AS VARCHAR)                         AS actor_id,
        CAST(actor->>'name' AS VARCHAR)                       AS actor_name,
        TRY_CAST(ctx AS JSON)                                 AS ctx,
        TRY_CAST(payload AS JSON)                             AS payload
      FROM read_json_auto('${globPattern}', format='newline_delimited', ignore_errors=true)
      WHERE id IS NOT NULL AND type IS NOT NULL AND ts IS NOT NULL
    `);

    const elapsedMs = Date.now() - startMs;

    // Count after
    const afterResult = await conn.run(`SELECT COUNT(*) as cnt FROM events`);
    const afterRows = await fetchAllRows(afterResult);
    const countAfter = Number(afterRows[0]?.cnt ?? 0);
    const inserted = countAfter - countBefore;
    const skipped = totalSource - inserted;

    console.log(`\n✅ Migration complete in ${(elapsedMs / 1000).toFixed(1)}s`);
    console.log(`   Inserted:  ${inserted} events`);
    console.log(`   Skipped:   ${skipped} (already existed)`);
    console.log(`   Total now: ${countAfter} events in DB`);

    // Show time range
    const rangeResult = await conn.run(`SELECT MIN(ts) as oldest, MAX(ts) as newest FROM events`);
    const rangeRows = await fetchAllRows(rangeResult);
    if (rangeRows[0]) {
      console.log(`   Time range: ${rangeRows[0].oldest} → ${rangeRows[0].newest}`);
    }

    // Show top event types
    console.log('\n📈 Top event types:');
    const topTypesResult = await conn.run(`
      SELECT type, COUNT(*) as cnt
      FROM events
      GROUP BY type
      ORDER BY cnt DESC
      LIMIT 10
    `);
    const topTypes = await fetchAllRows(topTypesResult);
    for (const row of topTypes) {
      console.log(`   ${String(row.cnt).padStart(6)}  ${row.type}`);
    }

  } finally {
    conn.closeSync();
  }
}

// ─── DuckDB row reader helper ─────────────────────────────────────────────────

async function fetchAllRows(result) {
  return result.getRowObjects();
}

// ─── Run ──────────────────────────────────────────────────────────────────────

migrate().catch((err) => {
  console.error('❌ Migration failed:', err.message ?? err);
  process.exit(1);
});
