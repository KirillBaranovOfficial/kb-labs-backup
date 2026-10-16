import { describe, expect, it, vi } from 'vitest';
import { chmodSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { once } from 'node:events';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { EnvironmentProvisioningError, EnvironmentTeardownError } from '@kb-labs/sdk/adapters';
import { DockerEnvironmentAdapter } from './index.js';

describe('DockerEnvironmentAdapter', () => {
  it('classifies daemon, image, and start failures at their provisioning phase', async () => {
    const daemonFailure = new DockerEnvironmentAdapter({
      execDocker: async () => { throw new Error('daemon socket unavailable'); },
    });
    const daemonReservation = await daemonFailure.reserve({ runId: 'daemon-failure' });
    await expect(daemonFailure.start(daemonReservation.environmentId, {
      workspacePath: '/repo', executionPolicy: { mounts: [], environment: {} },
    })).rejects.toMatchObject({ failureCause: 'daemon-unreachable', image: 'node:20-alpine' });

    const imageFailure = new DockerEnvironmentAdapter({
      defaultImage: 'registry.example.test/plugin:1.2.3',
      execDocker: async ([command]) => {
        if (command === 'info') return 'ok';
        if (command === 'image') throw new Error('image missing');
        if (command === 'pull') throw new Error('pull failed');
        throw new Error('unexpected start');
      },
    });
    const imageReservation = await imageFailure.reserve({ runId: 'image-failure' });
    await expect(imageFailure.start(imageReservation.environmentId, {
      workspacePath: '/repo', executionPolicy: { mounts: [], environment: {} },
    })).rejects.toMatchObject({ failureCause: 'image-unavailable', image: 'registry.example.test/plugin:1.2.3' });

  });

  it('classifies an attached docker run failure after preflight', async () => {
    const directory = mkdtempSync(join(tmpdir(), 'docker-run-failure-'));
    const dockerBinary = join(directory, 'docker');
    writeFileSync(dockerBinary, '#!/usr/bin/env node\nprocess.stderr.write("daemon rejected attached run\\n"); process.exit(1);\n');
    chmodSync(dockerBinary, 0o755);
    const execDocker = vi.fn(async ([command]: string[]) => command === 'ps' ? '' : 'ok');
    const adapter = new DockerEnvironmentAdapter({ dockerBinary, execDocker });
    const { environmentId } = await adapter.reserve({ runId: 'attached-run-failure' });

    try {
      await expect(adapter.start(environmentId, {
        workspacePath: '/repo', executionPolicy: { mounts: [], environment: {} },
      })).rejects.toMatchObject({ failureCause: 'daemon-error', image: 'node:20-alpine' });
      expect(execDocker.mock.calls.map(([args]) => args[0])).toEqual(['info', 'image', 'rm', 'ps', 'info', 'image']);
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  });

  it('tears down a provisioned child when it exits before emitting readiness output', async () => {
    const directory = mkdtempSync(join(tmpdir(), 'docker-run-no-ready-'));
    const dockerBinary = join(directory, 'docker');
    writeFileSync(dockerBinary, '#!/usr/bin/env node\nif (process.argv.includes("run")) process.exit(1);\n');
    chmodSync(dockerBinary, 0o755);
    const execDocker = vi.fn(async ([command]: string[]) => command === 'ps' ? '' : 'ok');
    const adapter = new DockerEnvironmentAdapter({ dockerBinary, execDocker });
    const { environmentId } = await adapter.reserve({ runId: 'attached-run-no-ready' });

    try {
      await expect(adapter.start(environmentId, {
        workspacePath: '/repo', executionPolicy: { mounts: [], environment: {} },
      })).rejects.toMatchObject({ failureCause: 'daemon-error' });
      expect(execDocker.mock.calls.map(([args]) => args)).toContainEqual([
        'rm', '-f', expect.stringMatching(/^kb-env-attached-run-no-ready-/),
      ]);
    } finally {
      await adapter.destroy(environmentId);
      rmSync(directory, { recursive: true, force: true });
    }
  });

  // case: S-244.1
  it('closes attached stdin and removes the container before waiting for the Docker client', async () => {
    const directory = mkdtempSync(join(tmpdir(), 'docker-run-ready-'));
    const dockerBinary = join(directory, 'docker');
    writeFileSync(dockerBinary, '#!/usr/bin/env node\nprocess.on("SIGTERM", () => {}); process.stdout.write("attached-runtime-ready" + String.fromCharCode(10)); process.stdin.resume(); process.stdin.on("end", () => process.exit(0));\n');
    chmodSync(dockerBinary, 0o755);
    const execDocker = vi.fn(async ([command]: string[]) => {
      if (command === 'ps') { return ''; }
      return 'ok';
    });
    const adapter = new DockerEnvironmentAdapter({ dockerBinary, execDocker });
    const { environmentId } = await adapter.reserve({ runId: 'attached-run-ready' });

    try {
      await adapter.start(environmentId, {
        workspacePath: '/repo', executionPolicy: { mounts: [], environment: {} },
      });
      const stream = adapter.getExecutionStream(environmentId);
      const [chunk] = await once(stream.readable, 'data', { signal: AbortSignal.timeout(1000) }) as [Buffer];
      expect(chunk.toString()).toBe('attached-runtime-ready\n');
      const teardown = adapter.destroy(environmentId);
      let timeout: ReturnType<typeof setTimeout> | undefined;
      const outcome = await Promise.race([
        teardown.then(() => 'finished'),
        new Promise<'hung'>((resolve) => { timeout = setTimeout(() => resolve('hung'), 500); }),
      ]);
      clearTimeout(timeout);
      if (outcome === 'hung') { stream.writable.end(); }
      await teardown;
      expect(outcome).toBe('finished');
      await expect(stream.completion).resolves.toMatchObject({ code: 0, signal: null });
      expect(execDocker).toHaveBeenCalledWith(['rm', '-f', expect.stringMatching(/^kb-env-attached-run-ready-/)]);
      expect(execDocker.mock.calls.map(([args]) => args[0])).toEqual(['info', 'image', 'rm', 'ps']);
    } finally {
      await adapter.destroy(environmentId);
      rmSync(directory, { recursive: true, force: true });
    }
  }, 3000);

  // case: S-244.1
  it('retains the provisioned container reference when daemon teardown fails', async () => {
    const directory = mkdtempSync(join(tmpdir(), 'docker-run-remove-failure-'));
    const dockerBinary = join(directory, 'docker');
    writeFileSync(dockerBinary, '#!/usr/bin/env node\nprocess.on("SIGTERM", () => {}); process.stdout.write("ready" + String.fromCharCode(10)); process.stdin.resume(); process.stdin.on("end", () => {});\n');
    chmodSync(dockerBinary, 0o755);
    let removals = 0;
    let containerPresent = true;
    const execDocker = vi.fn(async (args: string[]) => {
      if (args[0] === 'rm' && ++removals === 1) { throw new Error('daemon unavailable'); }
      if (args[0] === 'rm') { containerPresent = false; }
      if (args[0] === 'ps') { return containerPresent ? 'container-id\n' : ''; }
      return 'ok';
    });
    const adapter = new DockerEnvironmentAdapter({ dockerBinary, execDocker });
    const { environmentId } = await adapter.reserve({ runId: 'attached-run-remove-failure' });

    try {
      await adapter.start(environmentId, {
        workspacePath: '/repo', executionPolicy: { mounts: [], environment: {} },
      });
      await expect(adapter.destroy(environmentId)).rejects.toMatchObject({
        name: 'EnvironmentTeardownError',
        containerRef: expect.stringMatching(/^kb-env-attached-run-remove-failure-/),
        daemonError: expect.objectContaining({ message: 'daemon unavailable' }),
      });
      await adapter.destroy(environmentId);
      const rmCalls = execDocker.mock.calls.map(([args]) => args).filter(([command]) => command === 'rm');
      expect(rmCalls).toHaveLength(2);
      expect(rmCalls[0]?.[2]).toMatch(/^kb-env-attached-run-remove-failure-/);
      expect(rmCalls[1]?.[2]).toBe(rmCalls[0]?.[2]);
    } finally {
      await adapter.destroy(environmentId);
      rmSync(directory, { recursive: true, force: true });
    }
  }, 8000);

  // case: S-244.1
  it('treats daemon-observed absence as successful teardown after an rm conflict', async () => {
    const directory = mkdtempSync(join(tmpdir(), 'docker-run-rm-race-'));
    const dockerBinary = join(directory, 'docker');
    writeFileSync(dockerBinary, '#!/usr/bin/env node\nprocess.stdout.write("ready" + String.fromCharCode(10)); process.stdin.resume(); process.stdin.on("end", () => process.exit(0));\n');
    chmodSync(dockerBinary, 0o755);
    let queries = 0;
    const execDocker = vi.fn(async ([command]: string[]) => {
      if (command === 'rm') { throw new Error('daemon reported removal in progress'); }
      if (command === 'ps') { return ++queries === 1 ? 'container-id\n' : ''; }
      return 'ok';
    });
    const adapter = new DockerEnvironmentAdapter({ dockerBinary, execDocker });
    const { environmentId } = await adapter.reserve({ runId: 'attached-run-rm-race' });

    try {
      await adapter.start(environmentId, {
        workspacePath: '/repo', executionPolicy: { mounts: [], environment: {} },
      });
      await expect(adapter.destroy(environmentId)).resolves.toBeUndefined();
      expect(execDocker).toHaveBeenCalledWith(['rm', '-f', expect.stringMatching(/^kb-env-attached-run-rm-race-/)]);
      expect(execDocker.mock.calls.filter(([args]) => args[0] === 'ps')).toHaveLength(2);
    } finally {
      await adapter.destroy(environmentId);
      rmSync(directory, { recursive: true, force: true });
    }
  }, 3000);

  // case: S-244.1
  it('returns a catalogued teardown failure when removal fails and the container remains', async () => {
    const directory = mkdtempSync(join(tmpdir(), 'docker-run-rm-stuck-'));
    const dockerBinary = join(directory, 'docker');
    writeFileSync(dockerBinary, '#!/usr/bin/env node\nprocess.stdout.write("ready" + String.fromCharCode(10)); process.stdin.resume(); process.stdin.on("end", () => process.exit(0));\n');
    chmodSync(dockerBinary, 0o755);
    const execDocker = vi.fn(async ([command]: string[]) => {
      if (command === 'rm') { throw new Error('daemon rejected removal'); }
      if (command === 'ps') { return 'container-id\n'; }
      return 'ok';
    });
    const adapter = new DockerEnvironmentAdapter({ dockerBinary, execDocker });
    const { environmentId } = await adapter.reserve({ runId: 'attached-run-rm-stuck' });

    try {
      await adapter.start(environmentId, {
        workspacePath: '/repo', executionPolicy: { mounts: [], environment: {} },
      });
      const destroyStartedAt = Date.now();
      await expect(adapter.destroy(environmentId)).rejects.toMatchObject({
        name: 'EnvironmentTeardownError',
        containerRef: expect.stringMatching(/^kb-env-attached-run-rm-stuck-/),
        daemonError: expect.objectContaining({ message: 'daemon rejected removal' }),
      });
      const teardownDurationMs = Date.now() - destroyStartedAt;
      expect(teardownDurationMs).toBeGreaterThanOrEqual(4_900);
      expect(teardownDurationMs).toBeLessThan(7_000);
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  }, 7000);

  it('creates environment via docker run with expected args', async () => {
    const execDocker = vi.fn(async (args: string[]) => {
      if (args[0] === 'run') {
        return 'container-123\n';
      }
      throw new Error(`Unexpected command: ${args.join(' ')}`);
    });

    const adapter = new DockerEnvironmentAdapter({
      defaultImage: 'node:20-alpine',
      workspace: { cwd: '/repo' },
      execDocker,
    });

    const env = await adapter.create({
      runId: 'RUN_MAIN',
      templateId: 'node-dev',
      env: { A: '1', B: '2' },
      command: ['sleep', '300'],
    });

    expect(env.environmentId).toBe('container-123');
    expect(env.provider).toBe('docker-cli');
    expect(env.status).toBe('ready');
    expect(env.metadata?.image).toBe('node:20-alpine');

    const runArgs = execDocker.mock.calls[0]?.[0] as string[];
    expect(runArgs[0]).toBe('run');
    expect(runArgs).toContain('-d');
    expect(runArgs).toContain('--rm');
    expect(runArgs).toContain('--network');
    expect(runArgs[runArgs.indexOf('--network') + 1]).toBe('none');
    expect(runArgs).toContain('-e');
    expect(runArgs).toContain('A=1');
    expect(runArgs).toContain('B=2');
    expect(runArgs).toContain('-v');
    expect(runArgs).toContain('/repo:/workspace');
    expect(runArgs).toContain('-w');
    expect(runArgs).toContain('/workspace');
    expect(runArgs).toContain('node:20-alpine');
    expect(runArgs.slice(-2)).toEqual(['sleep', '300']);
  });

  it('maps docker running state to ready', async () => {
    const execDocker = vi.fn(async (args: string[]) => {
      if (args[0] === 'inspect') {
        return 'running\n';
      }
      throw new Error(`Unexpected command: ${args.join(' ')}`);
    });

    const adapter = new DockerEnvironmentAdapter({ execDocker });
    const status = await adapter.getStatus('env-1');

    expect(status.environmentId).toBe('env-1');
    expect(status.status).toBe('ready');
    expect(status.reason).toBe('running');
  });

  it('uses runtimeCommand when request command is omitted', async () => {
    const execDocker = vi.fn(async (args: string[]) => {
      if (args[0] === 'run') {
        return 'container-keepalive\n';
      }
      throw new Error(`Unexpected command: ${args.join(' ')}`);
    });

    const adapter = new DockerEnvironmentAdapter({
      execDocker,
      runtimeCommand: ['node', '/app/dist/cli.cjs'],
    });
    await adapter.create({ runId: 'keepalive' });

    const runArgs = execDocker.mock.calls[0]?.[0] as string[];
    expect(runArgs.slice(-2)).toEqual(['node', '/app/dist/cli.cjs']);
  });

  it('uses image default when no command and no runtimeCommand', async () => {
    const execDocker = vi.fn(async (args: string[]) => {
      if (args[0] === 'run') {
        return 'container-default\n';
      }
      throw new Error(`Unexpected command: ${args.join(' ')}`);
    });

    const adapter = new DockerEnvironmentAdapter({ execDocker });
    await adapter.create({ runId: 'keepalive' });

    const runArgs = execDocker.mock.calls[0]?.[0] as string[];
    // Last arg is the image, no command appended
    expect(runArgs[runArgs.length - 1]).toBe('node:20-alpine');
  });

  it('returns terminated for missing containers on status', async () => {
    const execDocker = vi.fn(async () => {
      throw new Error('Error response from daemon: No such container: env-missing');
    });

    const adapter = new DockerEnvironmentAdapter({ execDocker });
    const status = await adapter.getStatus('env-missing');

    expect(status.status).toBe('terminated');
    expect(status.reason).toBe('container_not_found');
  });

  it('destroy is idempotent for missing containers', async () => {
    const execDocker = vi.fn(async ([command]: string[]) => {
      if (command === 'rm') { throw new Error('daemon rejected removal'); }
      return '';
    });

    const adapter = new DockerEnvironmentAdapter({ execDocker });
    await expect(adapter.destroy('env-missing')).resolves.toBeUndefined();
  });

  it('renews lease with requested ttl', async () => {
    const adapter = new DockerEnvironmentAdapter();
    const ttlMs = 45_000;
    const lease = await adapter.renewLease('env-1', ttlMs);

    const expiresAt = new Date(lease.expiresAt).getTime();
    const acquiredAt = new Date(lease.acquiredAt).getTime();
    expect(expiresAt - acquiredAt).toBe(ttlMs);
  });

  it('returns provider capabilities', () => {
    const adapter = new DockerEnvironmentAdapter();
    const capabilities = adapter.getCapabilities();

    expect(capabilities.supportsLeaseRenewal).toBe(true);
    expect(capabilities.supportsSnapshots).toBe(false);
    expect(capabilities.custom?.provider).toBe('docker-cli');
  });
});
