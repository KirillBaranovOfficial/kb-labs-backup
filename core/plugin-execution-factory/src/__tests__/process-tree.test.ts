import { describe, expect, it, vi } from 'vitest';
import { readFileSync } from 'node:fs';
import { terminateProcessTree, parseDarwinProcessTable, type ProcessTableSource } from '../backends/worker-pool/process-tree.js';

const fixturesDir = new URL('./fixtures/', import.meta.url);

describe('terminateProcessTree', () => {
  it('parses recorded Darwin captures with stable one-second start identities', () => {
    const captureOne = readFileSync(new URL('darwin-process-table-capture-1.txt', fixturesDir), 'utf8');
    const captureTwo = readFileSync(new URL('darwin-process-table-capture-2.txt', fixturesDir), 'utf8');
    const firstRecords = parseDarwinProcessTable(captureOne);
    const secondRecords = parseDarwinProcessTable(captureTwo);
    const first = firstRecords.find(record => record.pid === 1);
    const second = secondRecords.find(record => record.pid === 1);

    expect(captureOne[0]).toBe(' ');
    expect(captureOne).toContain('    \n');
    expect(first).toBeDefined();
    expect(second).toBeDefined();
    expect(first).toMatchObject({ pid: 1, ppid: 0 });
    expect(second).toMatchObject({ pid: 1, ppid: 0 });
    expect(first?.startIdentity).toBe(second?.startIdentity);

    const differentStart = parseDarwinProcessTable(
      captureTwo.replace('Wed Oct 14 19:52:10 2026', 'Wed Oct 14 19:52:11 2026'),
    ).find(record => record.pid === 1);
    expect(differentStart?.startIdentity).not.toBe(first?.startIdentity);
  });

  it('does not match a same-second identity when its parent changed', async () => {
    let reads = 0;
    const source: ProcessTableSource = {
      read: () => {
        reads++;
        return reads === 1
          ? [
            { pid: 12, ppid: 1, startIdentity: 'Wed Oct 14 19:52:10 2026' },
            { pid: 44, ppid: 12, startIdentity: 'Wed Oct 14 19:52:10 2026' },
          ]
          : [
            { pid: 12, ppid: 1, startIdentity: 'Wed Oct 14 19:52:10 2026' },
            { pid: 44, ppid: 99, startIdentity: 'Wed Oct 14 19:52:10 2026' },
          ];
      },
    };
    const kill = vi.spyOn(process, 'kill').mockImplementation(() => true);
    try {
      const result = await terminateProcessTree(12, source);
      expect(kill).toHaveBeenCalledWith(12, 'SIGKILL');
      expect(kill).not.toHaveBeenCalledWith(44, 'SIGKILL');
      expect(result).toMatchObject({ verified: false, reason: 'identity-unverified', descendants: [44] });
    } finally {
      kill.mockRestore();
    }
  });

  it('reports an enumeration failure instead of treating an unknown tree as empty', async () => {
    const source: ProcessTableSource = { read: () => { throw new Error('table denied'); } };
    const kill = vi.spyOn(process, 'kill').mockImplementation(() => true);
    try {
      const result = await terminateProcessTree(12, source);
      expect(kill).toHaveBeenCalledWith(12, 'SIGKILL');
      expect(result).toMatchObject({ verified: false, reason: 'enumeration-failed' });
    } finally {
      kill.mockRestore();
    }
  });

  it('does not signal a reused PID when its start identity changes', async () => {
    let reads = 0;
    const source: ProcessTableSource = {
      read: () => {
        reads++;
        if (reads === 1) {
          return [
            { pid: 12, ppid: 1, startIdentity: 'root-start' },
            { pid: 44, ppid: 12, startIdentity: 'child-start-a' },
          ];
        }
        return [
          { pid: 12, ppid: 1, startIdentity: 'root-start' },
          { pid: 44, ppid: 12, startIdentity: 'child-start-b' },
        ];
      },
    };
    const kill = vi.spyOn(process, 'kill').mockImplementation(() => true);
    try {
      const result = await terminateProcessTree(12, source);
      expect(kill).toHaveBeenCalledWith(12, 'SIGKILL');
      expect(kill).not.toHaveBeenCalledWith(44, 'SIGKILL');
      expect(result).toMatchObject({ verified: false, reason: 'identity-unverified', descendants: [44] });
    } finally {
      kill.mockRestore();
    }
  });

  it('reports a survivor as unverified when it outlives the verification bound', async () => {
    const source: ProcessTableSource = {
      read: () => [
        { pid: 12, ppid: 1, startIdentity: 'root-start' },
        { pid: 44, ppid: 12, startIdentity: 'child-start' },
      ],
    };
    const kill = vi.spyOn(process, 'kill').mockImplementation(() => true);
    try {
      const result = await terminateProcessTree(12, source, 1);
      expect(kill).toHaveBeenCalledWith(44, 'SIGKILL');
      expect(kill).toHaveBeenCalledWith(12, 'SIGKILL');
      expect(result).toMatchObject({ verified: false, reason: 'survivor-timeout', descendants: [44] });
    } finally {
      kill.mockRestore();
    }
  });

  it('does not match a process when either start identity is missing', async () => {
    const source: ProcessTableSource = {
      read: vi.fn()
        .mockReturnValueOnce([{ pid: 12, ppid: 1 }, { pid: 44, ppid: 12, startIdentity: 'child-start' }])
        .mockReturnValueOnce([{ pid: 12, ppid: 1 }, { pid: 44, ppid: 12 }])
        .mockReturnValue([{ pid: 12, ppid: 1 }, { pid: 44, ppid: 12 }]),
    };
    const kill = vi.spyOn(process, 'kill').mockImplementation(() => true);
    try {
      const result = await terminateProcessTree(12, source);
      expect(kill).not.toHaveBeenCalledWith(44, 'SIGKILL');
      expect(result).toMatchObject({ verified: false, reason: 'identity-unverified' });
    } finally {
      kill.mockRestore();
    }
  });
});
