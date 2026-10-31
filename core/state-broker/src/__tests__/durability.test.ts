import { spawn, type ChildProcess } from 'node:child_process';
import { once } from 'node:events';
import { mkdtemp, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import { createInterface } from 'node:readline';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
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

  it('keeps only the verified journal prefix before a malformed complete record', async () => {
    const root = await mkdtemp(path.join(tmpdir(), 'kb-state-broker-corrupt-'));
    const journal = path.join(root, 'state-broker', 'journal.jsonl');
    try {
      const first = await AppendOnlyJournalStateBroker.open(root);
      await first.set('prefix', 'kept', 60_000);
      await first.set('later', 'not recovered', 60_000);
      await first.stop();

      const records = (await readFile(journal, 'utf8')).trimEnd().split('\n');
      await writeFile(journal, `${records[0]}\n{"version":1,"operation":\n${records[1]}\n`, 'utf8');
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

  it('serializes overlapping writers across processes through replay and compaction', async () => {
    const root = await mkdtemp(path.join(tmpdir(), 'kb-state-broker-process-lock-'));
    const moduleUrl = new URL('../backends/append-only-journal.ts', import.meta.url).href;
    const vitestPath = createRequire(import.meta.url).resolve('vitest');
    const vitePath = createRequire(vitestPath).resolve('vite');
    const tsxLoader = createRequire(vitePath).resolve('tsx/esm');
    const childCode = `
      import { AppendOnlyJournalStateBroker } from ${JSON.stringify(moduleUrl)};
      process.stdout.write('attempting-open\\n');
      const broker = await AppendOnlyJournalStateBroker.open(process.env.JOURNAL_ROOT);
      if (process.env.JOURNAL_ROLE === 'first') {
        await broker.set('writer-one', 'acknowledged', 60000);
        for (let value = 0; value < 1000; value++) await broker.set('compact-me', value, 60000);
        process.stdout.write('locked\\n');
        await new Promise((resolve) => process.stdin.once('data', resolve));
      } else {
        process.stdout.write('locked-two\\n');
        await broker.set('writer-two', 'acknowledged', 60000);
      }
      await broker.stop();
      process.stdout.write('stopped\\n');
    `;
    const startChild = (role: string) => {
      const child = spawn(process.execPath, [
        '--import', tsxLoader, '--input-type=module', '-e', childCode,
      ], {
        env: { ...process.env, JOURNAL_ROOT: root, JOURNAL_ROLE: role },
        stdio: ['pipe', 'pipe', 'pipe'],
      });
      const stdout = child.stdout;
      const stdin = child.stdin;
      const stderr = child.stderr;
      if (!stdout || !stdin || !stderr) throw new Error('journal lock worker has no piped stdio');
      const lines = createInterface({ input: stdout });
      const seen = new Set<string>();
      const waiters = new Map<string, Array<{ resolve(): void; reject(error: Error): void }>>();
      const stderrChunks: Buffer[] = [];
      stderr.on('data', (chunk: Buffer) => stderrChunks.push(chunk));
      lines.on('line', (value) => {
        seen.add(value);
        for (const waiter of waiters.get(value) ?? []) waiter.resolve();
        waiters.delete(value);
      });
      child.on('exit', (code, signal) => {
        if (code === 0) return;
        const failure = new Error(`journal worker exited (${code ?? signal}): ${Buffer.concat(stderrChunks).toString('utf8')}`);
        for (const waiting of waiters.values()) {
          for (const waiter of waiting) waiter.reject(failure);
        }
        waiters.clear();
      });
      const waitForLine = (line: string) => {
        if (seen.has(line)) return Promise.resolve();
        return new Promise<void>((resolve, reject) => {
          const waiting = waiters.get(line) ?? [];
          waiting.push({ resolve, reject });
          waiters.set(line, waiting);
          child.once('error', (error) => reject(error));
        });
      };
      return { child, stdin, lines, seen, waitForLine };
    };

    const first = startChild('first');
    let second: ReturnType<typeof startChild> | undefined;
    try {
      await first.waitForLine('locked');
      second = startChild('second');
      await second.waitForLine('attempting-open');
      await new Promise((resolve) => setTimeout(resolve, 100));
      expect(second.seen.has('locked-two')).toBe(false);

      first.stdin.write('release\n');
      await first.waitForLine('stopped');
      await second.waitForLine('locked-two');
      await second.waitForLine('stopped');

      const restarted = await AppendOnlyJournalStateBroker.open(root);
      await expect(restarted.get('writer-one')).resolves.toBe('acknowledged');
      await expect(restarted.get('writer-two')).resolves.toBe('acknowledged');
      await expect(restarted.get('compact-me')).resolves.toBe(999);
      await restarted.stop();
    } finally {
      const stopChild = async (child: ChildProcess | undefined) => {
        if (!child || child.exitCode !== null || child.signalCode !== null) return;
        child.kill();
        await once(child, 'exit');
      };
      await Promise.all([stopChild(first.child), stopChild(second?.child)]);
      first.lines.close();
      second?.lines.close();
      await rm(root, { recursive: true, force: true });
    }
  });
});
