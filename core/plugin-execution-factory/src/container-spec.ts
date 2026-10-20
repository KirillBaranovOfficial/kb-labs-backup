import type { PermissionSpec } from '@kb-labs/plugin-contracts';
import { createErrorEnvelope, ErrorEnvelopeError, type EnvironmentExecutionPolicy } from '@kb-labs/core-platform';
import path from 'node:path';
import { selectPermittedEnvironment } from '@kb-labs/plugin-runtime';

export type ContainerExecutionSpec = EnvironmentExecutionPolicy;

export interface ContainerExecutionInputs {
  environment: Readonly<Record<string, string | undefined>>;
}

function staticMountPrefix(pattern: string): string {
  const normalized = pattern.replaceAll('\\', '/');
  const segments = normalized.split('/');
  const globSegment = segments.findIndex((segment) => segment.includes('*') || segment.includes('?'));
  if (globSegment < 0) {
    return pattern;
  }

  const prefixSegments = segments.slice(0, globSegment);
  const prefix = prefixSegments.join('/');
  if (prefix.length === 0) {
    return normalized.startsWith('/') ? '/' : '.';
  }
  return path.normalize(prefix);
}

export function assertContainerNetworkPermissionsSupported(permissions: PermissionSpec): void {
  const network = permissions.network;
  if (!(network?.fetch?.length || network?.tcp?.connect?.length || network?.ws?.connect?.length)) {
    return;
  }

  throw new ErrorEnvelopeError(createErrorEnvelope('KB_RUNTIME_CONTAINER_NETWORK_UNSUPPORTED'));
}

/** Project the manifest grants into the data consumed by an environment adapter. */
export function createContainerExecutionSpec(
  permissions: PermissionSpec,
  inputs: ContainerExecutionInputs,
): ContainerExecutionSpec {
  const read = new Set(permissions.fs?.read ?? []);
  const write = new Set(permissions.fs?.write ?? []);
  const paths = new Set([...read, ...write].map(staticMountPrefix));
  const writablePrefixes = new Set([...write].map(staticMountPrefix));

  return {
    mounts: [...paths].sort().map((path) => ({
      path,
      access: writablePrefixes.has(path) ? 'read-write' : 'read-only',
    })),
    environment: selectPermittedEnvironment(permissions, inputs.environment),
  };
}
