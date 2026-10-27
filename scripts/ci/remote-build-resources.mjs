import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import os from 'node:os';

export const BUILD_MEMORY_BUDGET_FRACTION = 0.7;
export const BUILD_TASK_MEMORY_RESERVE_BYTES = 2.5 * 1024 ** 3;
const NODE_HEAP_BUDGET_FRACTION = 0.64;
const MEBIBYTE = 1024 ** 2;

export function deriveBuildLimits({ cpuCount, memTotalBytes }) {
  if (!Number.isSafeInteger(cpuCount) || cpuCount < 1) throw new Error('Runner CPU count must be a positive integer.');
  if (!Number.isSafeInteger(memTotalBytes) || memTotalBytes < 1) throw new Error('Runner MemTotal must be a positive integer.');
  const memoryBudgetBytes = Math.floor(memTotalBytes * BUILD_MEMORY_BUDGET_FRACTION);
  const memoryBoundConcurrency = Math.max(1, Math.floor(memoryBudgetBytes / BUILD_TASK_MEMORY_RESERVE_BYTES));
  const concurrency = Math.min(cpuCount, memoryBoundConcurrency);
  const nodeHeapMb = Math.floor((memoryBudgetBytes / concurrency * NODE_HEAP_BUDGET_FRACTION) / MEBIBYTE);
  return { cpuCount, memoryBudgetBytes, concurrency, nodeHeapMb };
}

export function readRunnerMemTotalBytes() {
  return parseMeminfo(readFileSync('/proc/meminfo', 'utf8')).totalBytes;
}

export function sampleRunnerResources({
  workspaceRoot,
  readMeminfo = () => process.platform === 'linux'
    ? readFileSync('/proc/meminfo', 'utf8')
    : `MemTotal: ${Math.floor(os.totalmem() / 1024)} kB\nMemAvailable: ${Math.floor(os.freemem() / 1024)} kB\n`,
  run = (command, args) => execFileSync(command, args, { encoding: 'utf8' }),
  now = Date.now,
}) {
  const memory = parseMeminfo(readMeminfo());
  const diskOutput = run('df', ['-Pk', workspaceRoot]);
  const diskRows = diskOutput.trim().split(/\r?\n/);
  const diskFields = diskRows.at(-1)?.trim().split(/\s+/) ?? [];
  const availableBlocks = Number(diskFields[3]);
  if (!Number.isSafeInteger(availableBlocks)) throw new Error(`Could not parse available workspace disk from df output: ${diskOutput}`);
  const processOutput = run('ps', ['-eo', 'pid=,rss=,comm=', '--sort=-rss']);
  const topRss = processOutput.split(/\r?\n/).filter(Boolean).slice(0, 5).map((line) => {
    const match = line.trim().match(/^(\d+)\s+(\d+)\s+(.+)$/);
    if (!match) throw new Error(`Could not parse process RSS sample: ${line}`);
    return { pid: Number(match[1]), rssBytes: Number(match[2]) * 1024, command: match[3] };
  });
  return {
    sampledAt: new Date(now()).toISOString(),
    memory: { ...memory, usedBytes: memory.totalBytes - memory.availableBytes },
    disk: { availableBytes: availableBlocks * 1024, workspace: workspaceRoot },
    topRss,
  };
}

export function resourcePeaks(samples) {
  return samples.reduce((peaks, sample) => ({
    peakMemoryBytes: Math.max(peaks.peakMemoryBytes, sample.memory.usedBytes),
    minimumFreeDiskBytes: Math.min(peaks.minimumFreeDiskBytes, sample.disk.availableBytes),
  }), { peakMemoryBytes: 0, minimumFreeDiskBytes: Number.MAX_SAFE_INTEGER });
}

function parseMeminfo(content) {
  const fields = new Map(content.split(/\r?\n/).flatMap((line) => {
    const match = line.match(/^(MemTotal|MemAvailable):\s+(\d+)\s+kB$/);
    return match ? [[match[1], Number(match[2]) * 1024]] : [];
  }));
  const totalBytes = fields.get('MemTotal');
  const availableBytes = fields.get('MemAvailable');
  if (!Number.isSafeInteger(totalBytes) || !Number.isSafeInteger(availableBytes)) throw new Error('Linux /proc/meminfo must contain MemTotal and MemAvailable.');
  return { totalBytes, availableBytes };
}
