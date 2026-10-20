import type { Readable, Writable } from 'node:stream';

/**
 * @module @kb-labs/core-platform/environment/environment-provider
 * Environment lifecycle abstraction.
 *
 * This port manages long-lived execution environments (container/pod/vm/sandbox).
 * It intentionally does NOT execute plugin jobs directly.
 */

/**
 * Environment status in lifecycle.
 */
export type EnvironmentStatus =
  | "pending"
  | "provisioning"
  | "ready"
  | "degraded"
  | "terminating"
  | "terminated"
  | "failed";

/**
 * Environment resource limits.
 */
export interface EnvironmentResources {
  cpu?: string;
  memory?: string;
  disk?: string;
  gpu?: string;
}

/**
 * Environment lease metadata.
 */
export interface EnvironmentLease {
  leaseId: string;
  acquiredAt: string;
  expiresAt: string;
  owner?: string;
}

/**
 * Environment network endpoint descriptor.
 */
export interface EnvironmentEndpoint {
  name: string;
  protocol?: "http" | "https" | "tcp" | "udp" | "unix";
  host?: string;
  port?: number;
  path?: string;
}

/**
 * Request to reserve an environment slot (phase 1 of two-phase provisioning).
 */
export interface ReserveEnvironmentRequest {
  namespace?: string;
  runId?: string;
  ttlMs?: number;
  metadata?: Record<string, unknown>;
}

/**
 * Result of a successful reservation (phase 1).
 */
export interface ReservedEnvironment {
  environmentId: string;
}

/**
 * Request to start a reserved environment (phase 2 of two-phase provisioning).
 */
export interface StartEnvironmentRequest {
  image?: string;
  workspacePath: string;
  executionPolicy: EnvironmentExecutionPolicy;
  metadata?: Record<string, unknown>;
}

/** OS-level container boundaries projected from a plugin's PermissionSpec. */
export interface EnvironmentExecutionPolicy {
  mounts: Array<{ path: string; access: 'read-only' | 'read-write' }>;
  environment: Record<string, string>;
}

/** Attached control stream exposed by an environment started for plugin execution. */
export interface EnvironmentExecutionStream {
  readable: Readable;
  writable: Writable;
  diagnostics: Readable;
  completion: Promise<{ code: number | null; signal: NodeJS.Signals | null }>;
}

export type EnvironmentProvisioningFailureCause =
  | 'daemon-unreachable'
  | 'image-unavailable'
  | 'daemon-error';

/** Typed failure emitted by an environment provider while provisioning a container. */
export class EnvironmentProvisioningError extends Error {
  constructor(
    readonly failureCause: EnvironmentProvisioningFailureCause,
    readonly image: string,
    readonly originalError: unknown,
  ) {
    super(`Container provisioning failed (${failureCause}) for image ${image}`);
    this.name = 'EnvironmentProvisioningError';
  }
}

/** Typed failure emitted when an environment provider cannot confirm container removal. */
export class EnvironmentTeardownError extends Error {
  constructor(
    readonly containerRef: string,
    readonly daemonError: unknown,
  ) {
    super(`Container teardown failed for ${containerRef}`);
    this.name = 'EnvironmentTeardownError';
  }
}

/**
 * Environment creation request.
 */
export interface CreateEnvironmentRequest {
  tenantId?: string;
  namespace?: string;
  runId?: string;
  templateId?: string;
  image?: string;
  workspacePath?: string;
  command?: string[];
  env?: Record<string, string>;
  resources?: EnvironmentResources;
  ttlMs?: number;
  metadata?: Record<string, unknown>;
}

/**
 * Created environment descriptor.
 */
export interface EnvironmentDescriptor {
  environmentId: string;
  provider: string;
  status: EnvironmentStatus;
  createdAt: string;
  updatedAt: string;
  lease?: EnvironmentLease;
  endpoints?: EnvironmentEndpoint[];
  metadata?: Record<string, unknown>;
}

/**
 * Environment status result.
 */
export interface EnvironmentStatusResult {
  environmentId: string;
  status: EnvironmentStatus;
  reason?: string;
  updatedAt: string;
  lease?: EnvironmentLease;
}

/**
 * Environment provider capabilities.
 */
export interface EnvironmentProviderCapabilities {
  supportsLeaseRenewal?: boolean;
  supportsSnapshots?: boolean;
  supportsExecProbe?: boolean;
  supportsLogs?: boolean;
  custom?: Record<string, unknown>;
}

/**
 * Long-lived environment provider abstraction.
 *
 * Responsibilities:
 * - Provision, inspect, and destroy environments
 * - Manage lease lifecycle
 *
 * Non-responsibilities:
 * - Plugin/job dispatch and horizontal runner scaling (ExecutionBackend)
 */
export interface IEnvironmentProvider {
  /**
   * Create a new environment instance.
   */
  create(request: CreateEnvironmentRequest): Promise<EnvironmentDescriptor>;

  /**
   * Get current status for an environment.
   */
  getStatus(environmentId: string): Promise<EnvironmentStatusResult>;

  /**
   * Destroy environment and release resources.
   * Must be idempotent.
   */
  destroy(environmentId: string, reason?: string): Promise<void>;

  /**
   * Reserve an environment slot before starting (two-phase provisioning).
   * Optional — only providers that support explicit reservation need to implement this.
   */
  reserve?(request: ReserveEnvironmentRequest): Promise<ReservedEnvironment>;

  /**
   * Start a reserved environment (second phase of two-phase provisioning).
   * Optional — only providers that support explicit reservation need to implement this.
   */
  start?(environmentId: string, request: StartEnvironmentRequest): Promise<EnvironmentDescriptor>;

  /** Return the attached child stdio stream for a started execution environment. */
  getExecutionStream(environmentId: string): EnvironmentExecutionStream;

  /**
   * Renew environment lease (if provider supports it).
   */
  renewLease?(environmentId: string, ttlMs: number): Promise<EnvironmentLease>;

  /**
   * Provider capability declaration.
   */
  getCapabilities?(): EnvironmentProviderCapabilities;
}
