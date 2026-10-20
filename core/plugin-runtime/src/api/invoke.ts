/**
 * Invoke API implementation
 */

import type {
  ExecutionTarget,
  InvokeAPI,
  InvokeOptions,
  PermissionSpec,
} from '@kb-labs/plugin-contracts';
import { matchesPermissionPattern, PermissionError } from '@kb-labs/plugin-contracts';

/**
 * Plugin invoker function type
 */
export type PluginInvokerFn = <T = unknown>(
  pluginId: string,
  input?: unknown,
  options?: InvokeOptions
) => Promise<T>;

export interface CreateInvokeAPIOptions {
  pluginId: string;
  permissions: PermissionSpec;
  invoker: PluginInvokerFn;
  auditTargetExecution?: (params: {
    method: 'invoke';
    target: ExecutionTarget;
    targetPluginId: string;
  }) => Promise<void> | void;
}

function checkTargetPermission(permissions: PermissionSpec, pluginId: string, invokeOptions?: InvokeOptions): void {
  const target = invokeOptions?.target;
  if (!target) {
    return;
  }

  if (!target.namespace) {
    throw new PermissionError('Target namespace is required when invoke target is specified', { plugin: pluginId, permission: 'platform.execution.targetUse' });
  }

  const executionPerms = permissions.platform?.execution;
  if (executionPerms === false || executionPerms === undefined) {
    throw new PermissionError(
      "Target execution denied: missing platform.execution.targetUse permission", { plugin: pluginId, permission: 'platform.execution.targetUse' }
    );
  }

  if (executionPerms === true) {
    return;
  }

  if (!executionPerms.targetUse) {
    throw new PermissionError(
      "Target execution denied: missing platform.execution.targetUse permission", { plugin: pluginId, permission: 'platform.execution.targetUse' }
    );
  }

  if (executionPerms.namespaces?.length) {
    const allowed = executionPerms.namespaces.some(pattern =>
      matchesPermissionPattern(target.namespace!, pattern)
    );
    if (!allowed) {
      throw new PermissionError(
        `Target namespace '${target.namespace}' denied: not in allowed execution namespaces scope`, { plugin: pluginId, permission: 'platform.execution.targetUse' }
      );
    }
  }
}

/**
 * Create InvokeAPI for calling other plugins
 */
export function createInvokeAPI(options: CreateInvokeAPIOptions): InvokeAPI {
  const { permissions, pluginId, invoker, auditTargetExecution } = options;

  // Check if invoke is allowed (empty array = disabled)
  const allowedPlugins = permissions.invoke?.allow ?? [];
  if (allowedPlugins.length === 0) {
    return {
      async call(): Promise<never> {
        throw new PermissionError('Plugin invocation not allowed', { plugin: pluginId, permission: 'invoke' });
      },
    };
  }

  return {
    async call<T = unknown>(
      pluginId: string,
      input?: unknown,
      invokeOptions?: InvokeOptions
    ): Promise<T> {
      checkTargetPermission(permissions, pluginId, invokeOptions);

      // Check plugin whitelist
      if (!allowedPlugins.some((pattern) => matchesPermissionPattern(pluginId, pattern))) {
        throw new PermissionError('Plugin not in whitelist', { plugin: pluginId, permission: 'invoke' });
      }

      const target = invokeOptions?.target;
      if (target) {
        try {
          await auditTargetExecution?.({
            method: 'invoke',
            target,
            targetPluginId: pluginId,
          });
        } catch {
          // Audit failures must never block execution.
        }
      }

      return invoker<T>(pluginId, input, invokeOptions);
    },
  };
}

/**
 * Create a no-op invoke API (for when invoke is disabled)
 */
export function createNoopInvokeAPI(pluginId: string): InvokeAPI {
  return {
    async call(): Promise<never> {
      throw new PermissionError('Plugin invocation not allowed', { plugin: pluginId, permission: 'invoke' });
    },
  };
}
