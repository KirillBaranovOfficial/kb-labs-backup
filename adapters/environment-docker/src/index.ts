/**
 * @module @kb-labs/adapters-environment-docker
 * Docker CLI adapter implementing IEnvironmentProvider.
 */

import { execFile, spawn, type ChildProcessWithoutNullStreams } from 'node:child_process';
import { PassThrough } from 'node:stream';
import { resolve, posix as posixPath } from 'node:path';
import { promisify } from 'node:util';
import { randomUUID } from 'node:crypto';
import { EnvironmentProvisioningError, EnvironmentTeardownError } from '@kb-labs/sdk/adapters';
import type {
  IEnvironmentProvider,
  CreateEnvironmentRequest,
  ReserveEnvironmentRequest,
  ReservedEnvironment,
  StartEnvironmentRequest,
  EnvironmentDescriptor,
  EnvironmentStatusResult,
  EnvironmentLease,
  EnvironmentProviderCapabilities,
  EnvironmentStatus,
  EnvironmentExecutionStream,
} from '@kb-labs/sdk/adapters';

export { manifest } from './manifest.js';

const execFileAsync = promisify(execFile);
const CONTAINER_TEARDOWN_TIMEOUT_MS = 5_000;
const CONTAINER_TEARDOWN_POLL_MS = 100;

interface WorkspaceContext {
  cwd?: string;
}

export type DockerExec = (args: string[]) => Promise<string>;

interface AttachedContainer {
  containerId: string;
  process: ChildProcessWithoutNullStreams;
  readable: PassThrough;
  diagnostics: PassThrough;
  completion: Promise<{ code: number | null; signal: NodeJS.Signals | null }>;
  ready: Promise<void>;
}

class DockerRunExitedBeforeCreation extends Error {
  constructor(
    readonly code: number | null,
    readonly signal: NodeJS.Signals | null,
    readonly diagnostics: string,
  ) {
    super(`Docker run exited before creating a container (code ${code ?? 'null'}, signal ${signal ?? 'none'})`);
  }
}

/**
 * Docker provider configuration.
 */
export interface DockerEnvironmentAdapterConfig {
  dockerBinary?: string;
  defaultImage?: string;
  autoRemove?: boolean;
  defaultTtlMs?: number;
  mountWorkspace?: boolean;
  workspaceMountPath?: string;
  workspace?: WorkspaceContext;
  /**
   * Optional command executor override (primarily for tests).
   * Receives argv for docker binary and must resolve stdout.
   */
  execDocker?: DockerExec;
  /**
   * Command to run inside the container to start the runtime server.
   * E.g. ['node', '/app/dist/cli.cjs']
   */
  runtimeCommand?: string[];
  /**
   * Extra host mappings passed as --add-host flags.
   * E.g. ['host.docker.internal:host-gateway'] for Linux dev environments
   * where host.docker.internal is not automatically available.
   */
  extraHosts?: string[];
}

/**
 * Docker CLI implementation of IEnvironmentProvider.
 */
export class DockerEnvironmentAdapter implements IEnvironmentProvider {
  private readonly dockerBinary: string;
  private readonly defaultImage: string;
  private readonly autoRemove: boolean;
  private readonly defaultTtlMs: number;
  private readonly mountWorkspace: boolean;
  private readonly workspaceMountPath: string;
  private readonly workspaceCwd?: string;
  private readonly dockerExec: DockerExec;
  private readonly runtimeCommand?: string[];
  private readonly extraHosts: string[];

  // reserved environments pending start(): environmentId → reservation metadata
  private readonly reserved = new Map<string, { runId: string; ttlMs: number; createdAt: Date }>();
  // active environments: environmentId → Docker containerId (for getStatus/destroy lookup)
  private readonly active = new Map<string, AttachedContainer>();

  constructor(private readonly config: DockerEnvironmentAdapterConfig = {}) {
    this.dockerBinary = config.dockerBinary ?? 'docker';
    this.defaultImage = config.defaultImage ?? 'node:20-alpine';
    this.autoRemove = config.autoRemove ?? true;
    this.defaultTtlMs = config.defaultTtlMs ?? 60 * 60 * 1000;
    this.mountWorkspace = config.mountWorkspace ?? true;
    this.workspaceMountPath = config.workspaceMountPath ?? '/workspace';
    this.workspaceCwd = config.workspace?.cwd;
    this.dockerExec = config.execDocker ?? this.defaultExecDocker.bind(this);
    this.runtimeCommand = config.runtimeCommand;
    this.extraHosts = config.extraHosts ?? [];
  }

  /**
   * Phase 1: Reserve an environmentId without starting the container.
   * The adapter generates a stable runtimeHostId used as environmentId.
   * When gateway config is present, this id will be used for JWT and RUNTIME_HOST_ID.
   */
  async reserve(request: ReserveEnvironmentRequest): Promise<ReservedEnvironment> {
    const runId = request.runId ?? randomUUID();
    const environmentId = `runtime-${runId}`;
    const ttlMs = request.ttlMs ?? this.defaultTtlMs;

    this.reserved.set(environmentId, { runId, ttlMs, createdAt: new Date() });

    return { environmentId };
  }

  /**
   * Phase 2: Start the container for a previously reserved environmentId.
   * Keeps Docker's attached stdio streams available for execution control and platform RPC.
   */
  async start(environmentId: string, request: StartEnvironmentRequest): Promise<EnvironmentDescriptor> {
    const reservation = this.reserved.get(environmentId);
    if (!reservation) {
      throw new Error(`No reservation found for environmentId: ${environmentId}. Call reserve() first.`);
    }
    this.reserved.delete(environmentId);

    const { runId, ttlMs } = reservation;
    const image = request.image ?? this.defaultImage;
    const containerName = this.buildContainerName(runId);
    const args = ['run', '-i', '--rm'];

    args.push('--name', containerName);
    args.push('--label', `kb.run_id=${runId}`);
    args.push('--label', `kb.environment_id=${environmentId}`);

    args.push('--network', 'none');

    for (const host of this.extraHosts) {
      args.push('--add-host', host);
    }

    for (const [key, value] of Object.entries(request.executionPolicy.environment)) {
      args.push('-e', `${key}=${value}`);
    }
    const workspacePath = request.workspacePath;
    for (const mount of request.executionPolicy.mounts) {
      const source = mount.path.startsWith('/')
        ? mount.path
        : resolve(workspacePath, mount.path);
      const target = mount.path.startsWith('/')
        ? mount.path
        : posixPath.join(this.workspaceMountPath, mount.path);
      args.push('--mount', `type=bind,source=${source},target=${target}${mount.access === 'read-only' ? ',readonly' : ''}`);
    }
    if (request.executionPolicy.mounts.some((mount) => mount.path === '.' || mount.path === './')) {
      args.push('-w', this.workspaceMountPath);
    }

    args.push(image);

    const command = this.runtimeCommand;
    if (command && command.length > 0) {
      args.push(...command);
    }

    try {
      await this.execDocker(['info']);
    } catch (error) {
      throw new EnvironmentProvisioningError('daemon-unreachable', image, error);
    }

    try {
      await this.execDocker(['image', 'inspect', image]);
    } catch (inspectError) {
      try {
        await this.execDocker(['pull', image]);
      } catch (error) {
        throw new EnvironmentProvisioningError(
          'image-unavailable',
          image,
          new AggregateError([inspectError, error], `Unable to inspect or pull container image ${image}`),
        );
      }
    }

    let attached: AttachedContainer;
    try {
      attached = await this.spawnContainer(args, containerName);
    } catch (error) {
      throw await this.classifyRunFailure(image, error);
    }

    const { containerId } = attached;
    // Register immediately after the Docker client starts. If readiness fails
    // or the caller aborts startup, the provisioning owner can still tear it down.
    this.active.set(environmentId, attached);

    try {
      await attached.ready;
    } catch (error) {
      try {
        await this.destroy(environmentId);
      } catch (cleanupError) {
        throw new AggregateError([error, cleanupError], 'Container failed before becoming ready and teardown also failed');
      }
      throw await this.classifyRunFailure(image, error);
    }

    const now = new Date();
    const lease = this.buildLease(now, ttlMs, runId);

    return {
      environmentId,
      provider: 'docker-cli',
      status: 'ready',
      createdAt: now.toISOString(),
      updatedAt: now.toISOString(),
      lease,
      metadata: {
        image,
        containerName,
        containerId,
      },
    };
  }

  /**
   * Single-phase create (legacy / simple use cases without gateway).
   */
  async create(request: CreateEnvironmentRequest): Promise<EnvironmentDescriptor> {
    const image = request.image ?? this.defaultImage;
    const ttlMs = request.ttlMs ?? this.defaultTtlMs;
    const runId = request.runId ?? 'run';

    const containerName = this.buildContainerName(runId);
    const args = ['run', '-d'];

    if (this.autoRemove) {
      args.push('--rm');
    }

    args.push('--name', containerName);
    args.push('--label', `kb.run_id=${runId}`);
    if (request.templateId) {
      args.push('--label', `kb.template_id=${request.templateId}`);
    }

    args.push('--network', 'none');

    for (const host of this.extraHosts) {
      args.push('--add-host', host);
    }

    for (const [key, value] of Object.entries(request.env ?? {})) {
      args.push('-e', `${key}=${value}`);
    }

    const workspacePath = request.workspacePath ?? this.workspaceCwd;
    if (workspacePath && this.mountWorkspace) {
      args.push('-v', `${workspacePath}:${this.workspaceMountPath}`);
      args.push('-w', this.workspaceMountPath);
    }

    args.push(image);

    if (request.command && request.command.length > 0) {
      args.push(...request.command);
    } else if (this.runtimeCommand && this.runtimeCommand.length > 0) {
      args.push(...this.runtimeCommand);
    }

    const containerId = (await this.execDocker(args)).trim();

    const now = new Date();
    const lease = this.buildLease(now, ttlMs, runId);

    return {
      environmentId: containerId,
      provider: 'docker-cli',
      status: 'ready',
      createdAt: now.toISOString(),
      updatedAt: now.toISOString(),
      lease,
      metadata: {
        image,
        containerName,
        templateId: request.templateId,
      },
    };
  }

  async getStatus(environmentId: string): Promise<EnvironmentStatusResult> {
    const now = new Date().toISOString();
    // Resolve logical environmentId → actual Docker container ID (from start())
    const dockerRef = this.active.get(environmentId)?.containerId ?? environmentId;

    try {
      const state = (await this.execDocker([
        'inspect',
        '-f',
        '{{.State.Status}}',
        dockerRef,
      ])).trim();

      return {
        environmentId,
        status: this.mapDockerStateToStatus(state),
        updatedAt: now,
        reason: state,
      };
    } catch (error) {
      if (this.isMissingContainerError(error)) {
        return {
          environmentId,
          status: 'terminated',
          updatedAt: now,
          reason: 'container_not_found',
        };
      }
      throw error;
    }
  }

  async destroy(environmentId: string): Promise<void> {
    // Clean up any pending reservation
    this.reserved.delete(environmentId);
    // Resolve logical environmentId → actual Docker container ID (from start())
    const active = this.active.get(environmentId);
    const dockerRef = active?.containerId ?? environmentId;

    // First let a cooperative runtime observe EOF, then ask the daemon to
    // force-remove the container. Docker --rm can race this request, so the
    // teardown result is the daemon-observed postcondition, not rm's exit code.
    if (active && active.process.exitCode === null && active.process.signalCode === null) {
      active.process.stdin.end();
    }

    let daemonFailure: unknown;
    try {
      await this.execDocker(['rm', '-f', dockerRef]);
    } catch (error) {
      daemonFailure = error;
    }
    const deadline = Date.now() + CONTAINER_TEARDOWN_TIMEOUT_MS;
    const presenceFilter = active
      ? ['--filter', `label=kb.environment_id=${environmentId}`]
      : ['--filter', `id=${dockerRef}`];
    let present = true;
    while (Date.now() < deadline) {
      try {
        const containerIds = (await this.execDocker(['ps', '-a', '-q', ...presenceFilter])).trim();
        present = containerIds.length > 0;
        if (!present) { break; }
      } catch (error) {
        daemonFailure = error;
      }
      await new Promise<void>((resolvePoll) => setTimeout(resolvePoll, CONTAINER_TEARDOWN_POLL_MS));
    }

    if (present) {
      if (active && active.process.exitCode === null && active.process.signalCode === null) {
        active.process.kill('SIGKILL');
        await active.completion;
      }
      const cause = daemonFailure ?? new Error(`Docker daemon still reports container ${dockerRef}`);
      throw new EnvironmentTeardownError(dockerRef, cause);
    }

    if (active) { await active.completion; }
    this.active.delete(environmentId);
  }

  getExecutionStream(environmentId: string): EnvironmentExecutionStream {
    const active = this.active.get(environmentId);
    if (!active) {
      throw new Error(`No attached execution stream exists for environment ${environmentId}`);
    }
    return {
      readable: active.readable,
      writable: active.process.stdin,
      diagnostics: active.diagnostics,
      completion: active.completion,
    };
  }

  async renewLease(environmentId: string, ttlMs: number): Promise<EnvironmentLease> {
    const now = new Date();
    return this.buildLease(now, ttlMs, environmentId);
  }

  getCapabilities(): EnvironmentProviderCapabilities {
    return {
      supportsLeaseRenewal: true,
      supportsExecProbe: false,
      supportsLogs: false,
      supportsSnapshots: false,
      custom: {
        provider: 'docker-cli',
        supportsTwoPhaseProvisioning: true,
      },
    };
  }

  private async execDocker(args: string[]): Promise<string> {
    return this.dockerExec(args);
  }

  private async classifyRunFailure(image: string, error: unknown): Promise<EnvironmentProvisioningError> {
    if (!(error instanceof DockerRunExitedBeforeCreation)) {
      return new EnvironmentProvisioningError('daemon-error', image, error);
    }
    try {
      await this.execDocker(['info']);
    } catch (daemonError) {
      return new EnvironmentProvisioningError(
        'daemon-unreachable',
        image,
        new AggregateError([error, daemonError], error.diagnostics || error.message),
      );
    }
    try {
      await this.execDocker(['image', 'inspect', image]);
    } catch (inspectError) {
      try {
        await this.execDocker(['pull', image]);
      } catch (pullError) {
        return new EnvironmentProvisioningError(
          'image-unavailable',
          image,
          new AggregateError([error, inspectError, pullError], error.diagnostics || error.message),
        );
      }
    }
    return new EnvironmentProvisioningError('daemon-error', image, error);
  }

  private async spawnContainer(args: string[], containerId: string): Promise<AttachedContainer> {
    const child = spawn(this.dockerBinary, args, { stdio: ['pipe', 'pipe', 'pipe'] });
    const completion = new Promise<{ code: number | null; signal: NodeJS.Signals | null }>((resolveExit) => {
      child.once('exit', (code, signal) => resolveExit({ code, signal }));
    });
    let diagnostics = '';
    const captureDiagnostics = (chunk: Buffer) => { diagnostics += chunk.toString('utf8'); };
    child.stderr.on('data', captureDiagnostics);
    await new Promise<void>((resolveSpawn, rejectSpawn) => {
      child.once('spawn', () => resolveSpawn());
      child.once('error', rejectSpawn);
    });
    const readable = new PassThrough();
    const diagnosticStream = new PassThrough();
    let resolveReady!: () => void;
    let rejectReady!: (error: Error) => void;
    const ready = new Promise<void>((resolve, reject) => {
      resolveReady = resolve;
      rejectReady = reject;
    });
    const onReady = () => {
      child.off('exit', onExit);
      resolveReady();
    };
    const onExit = (code: number | null, signal: NodeJS.Signals | null) => {
      child.stdout.off('data', onReady);
      rejectReady(new DockerRunExitedBeforeCreation(code, signal, diagnostics));
    };
    child.stdout.once('data', onReady);
    child.once('exit', onExit);
    child.stdout.pipe(readable);
    if (diagnostics.length > 0) { diagnosticStream.write(diagnostics); }
    child.stderr.pipe(diagnosticStream);
    return { containerId, process: child, readable, diagnostics: diagnosticStream, completion, ready };
  }

  private async defaultExecDocker(args: string[]): Promise<string> {
    try {
      const { stdout } = await execFileAsync(this.dockerBinary, args, {
        encoding: 'utf8',
      });
      return stdout;
    } catch (error) {
      const details = error instanceof Error ? error.message : String(error);
      throw new Error(`Docker command failed: ${this.dockerBinary} ${args.join(' ')} :: ${details}`);
    }
  }

  private buildLease(now: Date, ttlMs: number, owner?: string): EnvironmentLease {
    return {
      leaseId: randomUUID(),
      acquiredAt: now.toISOString(),
      expiresAt: new Date(now.getTime() + ttlMs).toISOString(),
      owner,
    };
  }

  private buildContainerName(runId: string): string {
    const normalizedRunId = runId.toLowerCase().replace(/[^a-z0-9_-]/g, '-').slice(0, 32);
    const entropy = randomUUID().slice(0, 8);
    return `kb-env-${normalizedRunId}-${Date.now().toString(36)}-${entropy}`;
  }

  private mapDockerStateToStatus(state: string): EnvironmentStatus {
    switch (state) {
      case 'running':
        return 'ready';
      case 'created':
      case 'restarting':
      case 'paused':
        return 'provisioning';
      case 'exited':
      case 'dead':
        return 'terminated';
      default:
        return 'degraded';
    }
  }

  private isMissingContainerError(error: unknown): boolean {
    const message = error instanceof Error ? error.message.toLowerCase() : String(error).toLowerCase();
    return (
      message.includes('no such container') ||
      message.includes('no such object') ||
      message.includes('container not found')
    );
  }
}

/**
 * Create Docker environment adapter.
 */
export function createAdapter(config?: DockerEnvironmentAdapterConfig): DockerEnvironmentAdapter {
  return new DockerEnvironmentAdapter(config);
}

export default createAdapter;
