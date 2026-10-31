import { mkdtemp, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { AppendOnlyJournalStateBroker } from '../backends/append-only-journal.js';

describe('StateBroker durability', () => {
  it('restores a resolved value after the broker is recreated', async () => {
    const root = await mkdtemp(path.join(tmpdir(), 'kb-state-broker-roundtrip-'));
    try {
      const first = await AppendOnlyJournalStateBroker.open(root);
      await first.set('round-trip', { answer: 42 }, 60_000);
      await first.stop();

      const restarted = await AppendOnlyJournalStateBroker.open(root);
      await expect(restarted.get('round-trip')).resolves.toEqual({ answer: 42 });
      await restarted.stop();
    } finally {
      await rm(root, { recursive: true, force: true });
    }
  });

  it('replays absolute expiry using wall-clock time and keeps the current omitted-TTL default', async () => {
    const root = await mkdtemp(path.join(tmpdir(), 'kb-state-broker-ttl-'));
    try {
      const first = await AppendOnlyJournalStateBroker.open(root);
      await first.set('unexpired', 'kept', 60_000);
      await first.set('expired', 'gone', 20);
      await first.set('default-ttl', 'default');
      await new Promise((resolve) => setTimeout(resolve, 35));
      await first.stop();

      const restarted = await AppendOnlyJournalStateBroker.open(root);
      await expect(restarted.get('unexpired')).resolves.toBe('kept');
      await expect(restarted.get('expired')).resolves.toBeNull();
      await expect(restarted.get('default-ttl')).resolves.toBe('default');
      await restarted.stop();
    } finally {
      await rm(root, { recursive: true, force: true });
    }
  });

  it('keeps only the verified journal prefix after a partial record', async () => {
    const root = await mkdtemp(path.join(tmpdir(), 'kb-state-broker-corrupt-'));
    const journal = path.join(root, 'state-broker', 'journal.jsonl');
    try {
      const first = await AppendOnlyJournalStateBroker.open(root);
      await first.set('prefix', 'kept', 60_000);
      await first.set('later', 'not recovered', 60_000);
      await first.stop();

      const records = (await readFile(journal, 'utf8')).trimEnd().split('\n');
      await writeFile(journal, `${records[0]}\n{"partial":`, 'utf8');
      const restarted = await AppendOnlyJournalStateBroker.open(root);
      expect(restarted.journalWasCorrupt).toBe(true);
      await expect(restarted.get('prefix')).resolves.toBe('kept');
      await expect(restarted.get('later')).resolves.toBeNull();
      expect((await readFile(journal, 'utf8')).trimEnd().split('\n')).toHaveLength(1);
      await restarted.stop();
    } finally {
      await rm(root, { recursive: true, force: true });
    }
  });

  it('writes its journal beneath the supplied platform data root', async () => {
    const root = await mkdtemp(path.join(tmpdir(), 'kb-state-broker-root-'));
    try {
      const broker = await AppendOnlyJournalStateBroker.open(root);
      await broker.set('root', 'inside', 60_000);
      await broker.stop();
      expect(await readdir(path.join(root, 'state-broker'))).toContain('journal.jsonl');
    } finally {
      await rm(root, { recursive: true, force: true });
    }
  });

  it('compacts mutation history to the current snapshot', async () => {
    const root = await mkdtemp(path.join(tmpdir(), 'kb-state-broker-compaction-'));
    const journal = path.join(root, 'state-broker', 'journal.jsonl');
    try {
      const broker = await AppendOnlyJournalStateBroker.open(root);
      for (let value = 0; value < 1_000; value++) {
        await broker.set('current', value, 60_000);
      }
      await broker.stop();
      const records = (await readFile(journal, 'utf8')).trimEnd().split('\n');
      expect(records).toHaveLength(1);
      expect(JSON.parse(records[0]!).value).toBe(999);
    } finally {
      await rm(root, { recursive: true, force: true });
    }
  });
});
