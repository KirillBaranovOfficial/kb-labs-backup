/**
 * @module @kb-labs/plugin-runtime/__tests__/error-handling
 *
 * Tests for error handling and IPC error serialization.
 *
 * V3 error handling:
 * - Standardized error types with codes
 * - JSON serialization for IPC transport
 * - Stack trace preservation
 * - Error wrapping utilities
 */

import { describe, it, expect } from 'vitest';
import {
  PluginError,
  PermissionError,
  TimeoutError,
  AbortError,
  ConfigError,
  ValidationError,
  NotFoundError,
  RateLimitError,
  PlatformError,
  isPluginError,
} from '@kb-labs/plugin-contracts';

describe('Error Handling', () => {
  describe('PluginError base class', () => {
    it('should create error with message and code', () => {
      const error = new PluginError('Test error', 'KB_RUNTIME_COMMAND_FAILED');

      expect(error.message).toBe('Test error');
      expect(error.code).toBe('KB_RUNTIME_COMMAND_FAILED');
      expect(error.name).toBe('PluginError');
      expect(error).toBeInstanceOf(Error);
      expect(error).toBeInstanceOf(PluginError);
    });

    it('should store error details', () => {
      const error = new PluginError('Error with details', 'KB_RUNTIME_COMMAND_FAILED', {
        field: 'username',
        value: 'invalid',
      });

      expect(error.details).toEqual({
        field: 'username',
        value: 'invalid',
      });
    });

    it('should have stack trace', () => {
      const error = new PluginError('Error message', 'KB_RUNTIME_COMMAND_FAILED');

      expect(error.stack).toBeDefined();
      expect(error.stack).toContain('Error message');
    });
  });

  describe('Specialized error types', () => {
    it('should create PermissionError with correct code', () => {
      const error = new PermissionError('Access denied', { path: '/etc/passwd' });

      expect(error).toBeInstanceOf(PermissionError);
      expect(error).toBeInstanceOf(PluginError);
      expect(error.name).toBe('PermissionError');
      expect(error.code).toBe('KB_PLUGIN_PERMISSION_DENIED');
      expect(error.message).toBe('Access denied');
      expect(error.details?.path).toBe('/etc/passwd');
    });

    it('should create TimeoutError with correct code', () => {
      const error = new TimeoutError('Operation timed out');

      expect(error).toBeInstanceOf(TimeoutError);
      expect(error.name).toBe('TimeoutError');
      expect(error.code).toBe('KB_RUNTIME_TIMEOUT');
    });

    it('should create AbortError with default message', () => {
      const error = new AbortError();

      expect(error).toBeInstanceOf(AbortError);
      expect(error.name).toBe('AbortError');
      expect(error.code).toBe('KB_PLUGIN_ABORTED');
      expect(error.message).toBe('Operation aborted');
    });

    it('should create ConfigError with details', () => {
      const error = new ConfigError('Invalid configuration', {
        field: 'port',
        value: '-1',
      });

      expect(error).toBeInstanceOf(ConfigError);
      expect(error.code).toBe('KB_CONFIG_INVALID');
      expect(error.details?.field).toBe('port');
    });

    it('should create ValidationError', () => {
      const error = new ValidationError('Validation failed', {
        errors: 'Field required, Invalid format',
      });

      expect(error).toBeInstanceOf(ValidationError);
      expect(error.code).toBe('KB_RUNTIME_INPUT_INVALID');
    });

    it('should create NotFoundError', () => {
      const error = new NotFoundError('Resource not found', {
        resourceType: 'plugin',
        id: '@kb-labs/missing',
      });

      expect(error).toBeInstanceOf(NotFoundError);
      expect(error.code).toBe('KB_RUNTIME_RESOURCE_NOT_FOUND');
    });

    it('should create RateLimitError with retry info', () => {
      const error = new RateLimitError('Too many requests', 5000);

      expect(error).toBeInstanceOf(RateLimitError);
      expect(error.code).toBe('KB_RUNTIME_RATE_LIMITED');
      expect(error.retryAfterMs).toBe(5000);
      expect(error.details?.retryAfter).toBe('5s');
    });

    // case: S-203.6
    it('should create PlatformError details that satisfy the catalog template', () => {
      const error = new PlatformError('llm', 'LLM service unavailable', {
        statusCode: '503',
      });

      expect(error).toBeInstanceOf(PlatformError);
      expect(error.code).toBe('KB_HOST_ADAPTER_UNAVAILABLE');
      expect(error.service).toBe('llm');
      expect(error.details?.slot).toBe('llm');
      expect(error.details?.reason).toBe('LLM service unavailable');
      expect(error.details?.statusCode).toBe('503');
    });
  });

  describe('Error type guard', () => {
    it('identifies PluginError instances', () => {
      expect(isPluginError(new PluginError('Test', 'KB_RUNTIME_COMMAND_FAILED'))).toBe(true);
      expect(isPluginError(new PermissionError('Access denied'))).toBe(true);
      expect(isPluginError(new Error('Regular error'))).toBe(false);
      expect(isPluginError('string')).toBe(false);
      expect(isPluginError(null)).toBe(false);
    });
  });
});
