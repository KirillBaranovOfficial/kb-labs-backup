/**
 * Unified error envelope (ADR-0049) for config-command failures.
 *
 * Shared by `kb config get|set` (`cli/commands`) and the host's
 * `/api/v1/adapters` API (`services/host`, stage 6 second slice): both
 * validate and write through `@kb-labs/core-config`, so both map its
 * `ConfigWriteError` to the same catalog codes here rather than keeping two
 * copies of this mapping in sync.
 */

import { createErrorEnvelope, toErrorEnvelope, type ErrorEnvelope } from '@kb-labs/core-platform';
import { ConfigWriteError } from './user-config/write-user-config.js';
import { PluginSettingsSchemaLoadError } from './plugin-settings-schema.js';

function unknownKeyEnvelope(error: ConfigWriteError): ErrorEnvelope {
  const suggestion = error.details.suggestion;
  return createErrorEnvelope('KB_CONFIG_UNKNOWN_KEY', {
    details: {
      path: error.details.path ?? '',
      suggestion: suggestion
        ? `Did you mean "${replaceKey(error.details.path ?? '', error.details.key ?? '', suggestion)}"?`
        : 'No similar key was found.',
    },
    cause: error.details.knownKeys
      ? `"${error.details.parent || '(top level)'}" declares: ${error.details.knownKeys.split(',').join(', ')}`
      : undefined,
  });
}

function replaceKey(path: string, wrong: string, right: string): string {
  return path
    .split('.')
    .map((segment) => (segment === wrong ? right : segment))
    .join('.');
}

function suggestVariableName(path: string): string {
  const tail = path.split('.').slice(-2).join('_');
  return tail.replace(/[^A-Za-z0-9]+/g, '_').replace(/([a-z])([A-Z])/g, '$1_$2').toUpperCase() || 'MY_SECRET';
}

/** Build the unified envelope for a failed config command. Never contains values. */
export function configErrorEnvelope(error: unknown): ErrorEnvelope {
  if (error !== null && typeof error === 'object' &&
      (error as { code?: unknown }).code === 'KB_PROJECT_UNKNOWN') {
    const details = (error as { details?: Record<string, string> }).details ?? {};
    return createErrorEnvelope('KB_PROJECT_UNKNOWN', {
      details: { path: details.path ?? '' },
    });
  }
  if (error instanceof PluginSettingsSchemaLoadError) {
    return createErrorEnvelope('KB_PLUGIN_SETTINGS_SCHEMA_INVALID', {
      details: { plugin: error.plugin },
      cause: error.detail ?? error.message,
    });
  }
  if (!(error instanceof ConfigWriteError)) {
    return toErrorEnvelope(error);
  }
  if (error.details.reason === 'project-scope-path') {
    const command = error.details.command;
    return createErrorEnvelope('KB_CONFIG_INVALID', {
      details: { path: error.details.path ?? '' },
      message: `Configuration path ${error.details.path ?? '(empty)'} is not valid at project scope; project plugin settings must use plugins.<configSection>.<key>.`,
      hint: command
        ? `Run "${command}" to write the project setting.`
        : 'Project plugin settings must use plugins.<configSection>.<key>; repeat the command with the corrected path and original value.',
    });
  }
  switch (error.code) {
    case 'KB_PLUGIN_SETTINGS_INVALID':
      return createErrorEnvelope('KB_PLUGIN_SETTINGS_INVALID', {
        details: { path: error.details.path ?? '' },
        cause: error.details.cause ?? error.message,
      });
    case 'KB_PLUGIN_SETTINGS_SCHEMA_INVALID':
      return createErrorEnvelope('KB_PLUGIN_SETTINGS_SCHEMA_INVALID', {
        details: { plugin: error.details.plugin ?? 'unknown' },
        cause: error.details.cause ?? error.message,
      });
    case 'KB_CONFIG_UNKNOWN_KEY':
      return unknownKeyEnvelope(error);
    case 'KB_CONFIG_SECRET_MISSING':
      return createErrorEnvelope('KB_CONFIG_SECRET_MISSING', {
        details: { variable: error.details.variable ?? '', path: error.details.path ?? '' },
        cause: `${error.details.path ?? 'The value'} is a secret and refers to \${${error.details.variable ?? ''}}, which is not set in this environment.`,
      });
    case 'KB_CONFIG_SECRET_PLAINTEXT': {
      const path = error.details.path ?? '';
      const variable = suggestVariableName(path);
      const scope = path.startsWith('platform.adapters.') || path.startsWith('platform.adapterOptions.')
        ? '--scope platform '
        : '';
      return createErrorEnvelope('KB_CONFIG_SECRET_PLAINTEXT', {
        details: { path, example: `kb config set ${scope}${path} '\${${variable}}'` },
        cause:
          error.details.reason === 'secret-shape'
            ? 'The value has the shape of a credential.'
            : 'The key is declared secret, so its value must be an ${ENV_VAR} reference.',
      });
    }
    case 'KB_CONFIG_CONFLICT':
      return createErrorEnvelope('KB_CONFIG_CONFLICT', {
        details: { path: error.details.path ?? '' },
        cause: error.message,
      });
    case 'KB_ADAPTER_NOT_INSTALLED':
      return createErrorEnvelope('KB_ADAPTER_NOT_INSTALLED', {
        details: {
          package: error.details.package ?? '',
          ...(error.details.slot ? { slot: error.details.slot } : {}),
          ...(error.details.path ? { path: error.details.path } : {}),
        },
        ...(error.details.hint ? { hint: error.details.hint } : {}),
      });
    case 'KB_ADAPTER_SLOT_MISMATCH':
      return createErrorEnvelope('KB_ADAPTER_SLOT_MISMATCH', {
        details: {
          package: error.details.package ?? '',
          slot: error.details.slot ?? '',
          ...(error.details.path ? { path: error.details.path } : {}),
        },
        ...(error.details.hint ? { hint: error.details.hint } : {}),
      });
    case 'KB_CONFIG_INVALID':
      return createErrorEnvelope('KB_CONFIG_INVALID', {
        details: { path: error.details.path ?? '' },
        cause: error.message,
        ...(error.details.hint ? { hint: error.details.hint } : {}),
      });
  }
}
