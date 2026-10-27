import assert from 'node:assert/strict';
import test from 'node:test';
import { deriveBuildLimits, sampleRunnerResources } from './remote-build-resources.mjs';

// case: S-262.7
test('build concurrency and Node heap derive from runner CPU and memory within the declared budget', () => {
  assert.deepEqual(deriveBuildLimits({ cpuCount: 4, memTotalBytes: 16 * 1024 ** 3 }), {
    cpuCount: 4,
    memoryBudgetBytes: Math.floor(16 * 1024 ** 3 * 0.7),
    concurrency: 4,
    nodeHeapMb: 1835,
  });
  assert.equal(deriveBuildLimits({ cpuCount: 2, memTotalBytes: 4 * 1024 ** 3 }).concurrency, 1);
});

// case: S-262.7
test('resource sampler captures available memory, workspace disk, and top RSS processes', () => {
  const commands = [];
  const sample = sampleRunnerResources({
    workspaceRoot: '/workspace',
    readMeminfo: () => 'MemTotal:       16777216 kB\nMemAvailable:   12582912 kB\n',
    run: (command, args) => {
      commands.push([command, args]);
      if (command === 'df') return 'Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/root 100000 30000 70000 30% /workspace\n';
      return '2 2048 node\n1 1024 pnpm\n';
    },
    now: () => 1234,
  });

  assert.deepEqual(sample, {
    sampledAt: '1970-01-01T00:00:01.234Z',
    memory: { totalBytes: 17179869184, availableBytes: 12884901888, usedBytes: 4294967296 },
    disk: { availableBytes: 71680000, workspace: '/workspace' },
    topRss: [{ pid: 2, rssBytes: 2097152, command: 'node' }, { pid: 1, rssBytes: 1048576, command: 'pnpm' }],
  });
  assert.deepEqual(commands.map(([command]) => command), ['df', 'ps']);
});

// case: S-262.7
test('terminated build keeps an incrementally persisted partial result and resource sample', async () => {
  const { runStage } = await import('./remote-verify-stage.mjs');
  const { mkdtempSync, readFileSync, rmSync } = await import('node:fs');
  const os = await import('node:os');
  const path = await import('node:path');
  const temporaryRoot = mkdtempSync(path.join(os.tmpdir(), 'remote-build-partial-'));
  const resultPath = path.join(temporaryRoot, 'stage.json');
  const samplesPath = path.join(temporaryRoot, 'samples.jsonl');
  let complete;
  let pending;
  try {
    const expectedSample = { sampledAt: new Date(1000).toISOString(), memory: { totalBytes: 1000, availableBytes: 500, usedBytes: 500 }, disk: { availableBytes: 2000, workspace: temporaryRoot }, topRss: [] };
    pending = runStage({
      stage: 'workspace-build', packages: ['@fixture/a'], selectedPackageCount: 1,
      resourceSamplePath: samplesPath, partialResultPath: resultPath, sampleIntervalMs: 60_000,
      sampleResources: () => expectedSample,
      buildLimits: { cpuCount: 1, memoryBudgetBytes: 700, concurrency: 1, nodeHeapMb: 1 },
      runCommand: async () => new Promise((resolve) => { complete = resolve; }),
      now: (() => { let value = 1000; return () => value += 1; })(),
    });
    const partial = JSON.parse(readFileSync(resultPath, 'utf8'));
    const samples = readFileSync(samplesPath, 'utf8').trim().split('\n').map(JSON.parse);
    assert.equal(partial.name, 'workspace-build');
    assert.equal(partial.status, 'running');
    assert.ok(partial.durationMs >= 0);
    assert.deepEqual(partial.lastResourceSample, expectedSample);
    assert.deepEqual(samples, [expectedSample]);
    complete({ status: 1, stdout: '', stderr: 'stopped' });
    const finished = await pending;
    pending = undefined;
    assert.equal(finished.status, 'fail');
  } finally {
    if (pending && complete) {
      complete({ status: 1, stdout: '', stderr: 'test cleanup' });
      await pending;
    }
    rmSync(temporaryRoot, { recursive: true, force: true });
  }
});

// case: S-262.7
test('build fails visibly when its required resource sampler cannot produce evidence', async () => {
  const { runStage } = await import('./remote-verify-stage.mjs');
  const { mkdtempSync, rmSync } = await import('node:fs');
  const os = await import('node:os');
  const path = await import('node:path');
  const temporaryRoot = mkdtempSync(path.join(os.tmpdir(), 'remote-build-sampler-failure-'));
  try {
    const result = await runStage({
      stage: 'workspace-build', packages: ['@fixture/a'], selectedPackageCount: 1,
      resourceSamplePath: path.join(temporaryRoot, 'samples.jsonl'),
      partialResultPath: path.join(temporaryRoot, 'stage.json'),
      sampleResources: () => { throw new Error('df unavailable'); },
      buildLimits: { cpuCount: 1, memoryBudgetBytes: 700, concurrency: 1, nodeHeapMb: 1 },
      runCommand: async () => ({ status: 0, stdout: 'built', stderr: '' }),
    });
    assert.equal(result.status, 'fail');
    assert.ok(result.failures.some((failure) => failure.includes('resource sampling failed: df unavailable')));
  } finally {
    rmSync(temporaryRoot, { recursive: true, force: true });
  }
});
