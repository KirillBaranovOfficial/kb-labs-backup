/**
 * Error types for V3 Plugin System
 *
 * Standardized errors with codes for proper handling across IPC boundaries.
 */

import type { ErrorCode } from '@kb-labs/core-platform/error-envelope';
import {
  serializeError,
  toErrorEnvelope,
  type SerializedError,
} from '@kb-labs/core-platform';

/**
 * Base plugin error class
 */
export class PluginError extends Error {
  /**
   * Error code for programmatic handling
   */
  public readonly code: ErrorCode;

  /**
   * Additional error details
   */
  public readonly details?: Record<string, string>;
  public readonly hint?: string;

  constructor(
    message: string,
    code: ErrorCode,
    details?: Record<string, string>,
    hint?: string,
  ) {
    super(message);
    this.name = 'PluginError';
    this.code = code;
    this.details = details;
    this.hint = hint;

    // Ensure prototype chain is correct
    Object.setPrototypeOf(this, new.target.prototype);
  }

  /**
   * Convert to JSON-serializable object (for IPC)
   */
  toJSON(): SerializedPluginError {
    return {
      ...serializeError(this),
      name: this.name,
      message: this.message,
      code: this.code,
      details: this.details,
      hint: this.hint,
    };
  }

  /**
   * Create from serialized error (from IPC)
   */
  static fromJSON(data: SerializedPluginError): PluginError {
    const error = new PluginError(data.message, data.code, data.details, data.hint);
    error.name = data.name;
    error.stack = data.stack;
    return error;
  }
}

/**
 * Permission denied error
 */
export class PermissionError extends PluginError {
  constructor(message: string, details?: Record<string, string>) {
    super(message, 'KB_PLUGIN_PERMISSION_DENIED', details);
    this.name = 'PermissionError';
  }
}

/**
 * Operation timeout error
 */
export class TimeoutError extends PluginError {
  constructor(message: string, details?: Record<string, string>) {
    super(message, 'KB_RUNTIME_TIMEOUT', details);
    this.name = 'TimeoutError';
  }
}

/**
 * Operation aborted error (e.g., by signal)
 */
export class AbortError extends PluginError {
  constructor(message: string = 'Operation aborted') {
    super(message, 'KB_PLUGIN_ABORTED');
    this.name = 'AbortError';
  }
}

/**
 * Configuration error
 */
export class ConfigError extends PluginError {
  constructor(message: string, details?: Record<string, string>) {
    super(message, 'KB_CONFIG_INVALID', details);
    this.name = 'ConfigError';
  }
}

/**
 * Validation error
 */
export class ValidationError extends PluginError {
  constructor(message: string, details?: Record<string, string>) {
    super(message, 'KB_RUNTIME_INPUT_INVALID', details);
    this.name = 'ValidationError';
  }
}

/**
 * Not found error
 */
export class NotFoundError extends PluginError {
  constructor(message: string, details?: Record<string, string>) {
    super(message, 'KB_RUNTIME_RESOURCE_NOT_FOUND', details);
    this.name = 'NotFoundError';
  }
}

/**
 * Rate limit exceeded error
 */
export class RateLimitError extends PluginError {
  constructor(
    message: string = 'Rate limit exceeded',
    public readonly retryAfterMs?: number
  ) {
    super(message, 'KB_RUNTIME_RATE_LIMITED', retryAfterMs === undefined ? undefined : { retryAfter: `${Math.ceil(retryAfterMs / 1000)}s` });
    this.name = 'RateLimitError';
  }
}

/**
 * Platform service error (e.g., LLM, cache, storage)
 */
export class PlatformError extends PluginError {
  constructor(
    public readonly service: string,
    message: string,
    details?: Record<string, string>
  ) {
    super(message, 'KB_HOST_ADAPTER_UNAVAILABLE', { service, ...details });
    this.name = 'PlatformError';
  }
}

/**
 * Serialized error for IPC transport
 */
export type SerializedPluginError = SerializedError & {
  code: ErrorCode;
  details?: Record<string, string>;
  hint?: string;
};

/**
 * Check if an error is a PluginError
 */
export function isPluginError(error: unknown): error is PluginError {
  return error instanceof PluginError;
}

/**
 * Wrap any error as a PluginError
 */
export function wrapError(error: unknown, defaultCode: ErrorCode = 'KB_RUNTIME_UNEXPECTED'): PluginError {
  if (error instanceof PluginError) {
    return error;
  }

  if (error instanceof Error) {
    const envelope = toErrorEnvelope(error);
    const typedEnvelope = envelope.code !== 'KB_RUNTIME_UNEXPECTED';
    const wrapped = new PluginError(
      error.message,
      typedEnvelope ? envelope.code : defaultCode,
      typedEnvelope ? envelope.details : { originalName: error.name },
      typedEnvelope ? envelope.hint : undefined,
    );
    wrapped.name = error.name;
    wrapped.stack = error.stack;
    Object.defineProperty(wrapped, 'cause', { value: error, configurable: true });
    return wrapped;
  }

  return new PluginError(String(error), defaultCode);
}
