/**
 * @module @kb-labs/plugin-execution-factory/__tests__/errors
 *
 * Unit tests for execution layer error classes and type guards.
 */

import { describe, it, expect } from 'vitest';
import { AdapterCredentialsMissingError, serializeError } from '@kb-labs/core-platform';
import { normalizeError } from '../utils.js';
import {
  ExecutionLayerError,
  isExecutionLayerError,
  isKnownErrorCode,
  TimeoutError,
  AbortError,
  HandlerContractError,
  HandlerNotFoundError,
  WorkspaceError,
  PermissionDeniedError,
  ValidationError,
  QueueFullError,
  AcquireTimeoutError,
  WorkerCrashedError,
  WorkerUnhealthyError,
} from '../errors.js';

describe('isKnownErrorCode()', () => {
  it('returns true for all known codes', () => {
    const codes = [
      'TIMEOUT', 'ABORTED', 'PERMISSION_DENIED',
      'HANDLER_CONTRACT_ERROR', 'HANDLER_NOT_FOUND', 'WORKSPACE_ERROR',
      'VALIDATION_ERROR', 'UNKNOWN_ERROR',
      'QUEUE_FULL', 'ACQUIRE_TIMEOUT', 'WORKER_CRASHED', 'WORKER_UNHEALTHY',
    ];
    for (const code of codes) {
      expect(isKnownErrorCode(code)).toBe(true);
    }
  });

  it('returns false for unknown strings', () => {
    expect(isKnownErrorCode('MADE_UP')).toBe(false);
    expect(isKnownErrorCode('')).toBe(false);
  });

  it('returns false for non-strings', () => {
    expect(isKnownErrorCode(null)).toBe(false);
    expect(isKnownErrorCode(42)).toBe(false);
    expect(isKnownErrorCode(undefined)).toBe(false);
  });
});

describe('ExecutionLayerError', () => {
  it('sets name, message, code', () => {
    const err = new ExecutionLayerError('something failed', 'UNKNOWN_ERROR');
    expect(err.name).toBe('ExecutionLayerError');
    expect(err.message).toBe('something failed');
    expect(err.code).toBe('UNKNOWN_ERROR');
    expect(err instanceof Error).toBe(true);
  });

  it('defaults code to UNKNOWN_ERROR', () => {
    const err = new ExecutionLayerError('oops');
    expect(err.code).toBe('UNKNOWN_ERROR');
  });

  it('stores details', () => {
    const err = new ExecutionLayerError('fail', 'VALIDATION_ERROR', { field: 'name' });
    expect(err.details).toEqual({ field: 'name' });
  });

  it('uses the platform serialized error contract', () => {
    const err = new ExecutionLayerError('fail', 'TIMEOUT', { ms: 5000 });
    expect(serializeError(err)).toMatchObject({
      name: 'ExecutionLayerError',
      message: 'fail',
      stack: expect.any(String),
    });
  });
});

describe('normalizeError handler boundary', () => {
  // case: S-203.6
  it('serializes only declared execution error fields and keeps stack out of the envelope', () => {
    const cause = new Error('handler exploded');
    Object.assign(cause, {
      code: 'MIND_NOT_FOUND',
      hint: 'Choose a known mind.',
      details: { id: 'missing' },
      undocumented: 'must not cross the boundary',
    });

    const normalized = normalizeError(cause);

    expect(normalized).toMatchObject({
      code: 'MIND_NOT_FOUND',
      message: 'handler exploded',
      hint: 'Choose a known mind.',
      details: { id: 'missing' },
    });
    expect(normalized).not.toHaveProperty('stack');
    expect(normalized).not.toHaveProperty('undocumented');
  });

  // case: S-203.2
  it('retains a typed plugin product error with its hint and flat details', () => {
    const error = Object.assign(new Error('The plan is invalid.'), {
      code: 'COMMIT_PLAN_INVALID',
      hint: 'Run `kb commit generate` and inspect the proposed plan.',
      details: { reason: 'empty' },
    });
    const normalized = normalizeError(error);
    expect(normalized).toMatchObject({
      code: 'COMMIT_PLAN_INVALID',
      message: 'The plan is invalid.',
      hint: 'Run `kb commit generate` and inspect the proposed plan.',
      details: { reason: 'empty' },
    });
  });

  // case: S-054.5, S-054.8, S-203.2
  it('retains a catalogued adapter credentials error thrown by a handler', () => {
    const normalized = normalizeError(new AdapterCredentialsMissingError(
      'llm', 'OPENAI_API_KEY', 'platform.adapterOptions.llm.apiKey',
    ));
    expect(normalized).toMatchObject({
      code: 'KB_ADAPTER_CREDENTIALS_MISSING',
      message: 'Adapter llm is bound but is missing required credential OPENAI_API_KEY.',
      hint: expect.stringContaining('kb config set --scope platform platform.adapterOptions.llm.apiKey'),
      details: { service: 'llm', credential: 'OPENAI_API_KEY' },
      actions: [{ id: 'configure-adapter-credential', command: expect.stringContaining('OPENAI_API_KEY') }],
    });
  });

  // case: S-203.2, S-203.3
  it('normalizes an execution timeout for the unexpected plugin failure boundary', () => {
    expect(normalizeError(new TimeoutError('Timed out after 50ms', 50))).toMatchObject({
      code: 'KB_RUNTIME_UNEXPECTED',
      hint: expect.stringContaining('correlationId'),
    });
  });

  // case: S-203.2
  it('uses a platform unexpected code for an untyped thrown value instead of HANDLER_ERROR', () => {
    expect(normalizeError(new Error('private cause'))).toMatchObject({
      code: 'KB_RUNTIME_UNEXPECTED',
      message: 'An unexpected error occurred.',
    });
    expect(normalizeError(new Error('private cause')).code).not.toBe('HANDLER_ERROR');
  });
});

describe('isExecutionLayerError()', () => {
  it('returns true for ExecutionLayerError instances', () => {
    expect(isExecutionLayerError(new ExecutionLayerError('x'))).toBe(true);
    expect(isExecutionLayerError(new TimeoutError('x'))).toBe(true);
    expect(isExecutionLayerError(new AbortError())).toBe(true);
  });

  it('returns false for plain errors', () => {
    expect(isExecutionLayerError(new Error('plain'))).toBe(false);
    expect(isExecutionLayerError(null)).toBe(false);
    expect(isExecutionLayerError('string')).toBe(false);
  });
});

describe('TimeoutError', () => {
  it('sets name, code, timeoutMs', () => {
    const err = new TimeoutError('timed out', 3000);
    expect(err.name).toBe('TimeoutError');
    expect(err.code).toBe('TIMEOUT');
    expect(err.timeoutMs).toBe(3000);
  });

  it('works without timeoutMs', () => {
    const err = new TimeoutError('timed out');
    expect(err.timeoutMs).toBeUndefined();
  });
});

describe('AbortError', () => {
  it('has default message and ABORTED code', () => {
    const err = new AbortError();
    expect(err.name).toBe('AbortError');
    expect(err.code).toBe('ABORTED');
    expect(err.message).toBe('Execution aborted');
  });

  it('accepts custom message', () => {
    const err = new AbortError('user cancelled');
    expect(err.message).toBe('user cancelled');
  });
});

describe('HandlerContractError', () => {
  it('sets name and HANDLER_CONTRACT_ERROR code', () => {
    const err = new HandlerContractError('no execute fn');
    expect(err.name).toBe('HandlerContractError');
    expect(err.code).toBe('HANDLER_CONTRACT_ERROR');
  });
});

describe('HandlerNotFoundError', () => {
  it('includes path in message and details', () => {
    const err = new HandlerNotFoundError('/dist/cmd.js');
    expect(err.name).toBe('HandlerNotFoundError');
    expect(err.code).toBe('HANDLER_NOT_FOUND');
    expect(err.handlerPath).toBe('/dist/cmd.js');
    expect(err.message).toContain('/dist/cmd.js');
  });
});

describe('WorkspaceError / PermissionDeniedError / ValidationError', () => {
  it('WorkspaceError has correct code', () => {
    const err = new WorkspaceError('cannot lease');
    expect(err.code).toBe('WORKSPACE_ERROR');
    expect(err.name).toBe('WorkspaceError');
  });

  it('PermissionDeniedError has correct code', () => {
    const err = new PermissionDeniedError('no access', { resource: 'fs' });
    expect(err.code).toBe('PERMISSION_DENIED');
    expect(err.details).toEqual({ resource: 'fs' });
  });

  it('ValidationError has correct code', () => {
    const err = new ValidationError('bad input');
    expect(err.code).toBe('VALIDATION_ERROR');
  });
});

describe('Pool errors', () => {
  it('QueueFullError formats message with sizes', () => {
    const err = new QueueFullError(100, 100);
    expect(err.name).toBe('QueueFullError');
    expect(err.code).toBe('QUEUE_FULL');
    expect(err.queueSize).toBe(100);
    expect(err.maxQueueSize).toBe(100);
    expect(err.message).toContain('100/100');
  });

  it('AcquireTimeoutError formats message with ms', () => {
    const err = new AcquireTimeoutError(5000);
    expect(err.name).toBe('AcquireTimeoutError');
    expect(err.code).toBe('ACQUIRE_TIMEOUT');
    expect(err.acquireTimeoutMs).toBe(5000);
    expect(err.message).toContain('5000ms');
  });

  it('WorkerCrashedError formats message with exitCode and signal', () => {
    const err = new WorkerCrashedError('w-1', 1, 'SIGKILL');
    expect(err.name).toBe('WorkerCrashedError');
    expect(err.code).toBe('WORKER_CRASHED');
    expect(err.workerId).toBe('w-1');
    expect(err.exitCode).toBe(1);
    expect(err.signal).toBe('SIGKILL');
    expect(err.message).toContain('w-1');
    expect(err.message).toContain('SIGKILL');
  });

  it('WorkerCrashedError works without exitCode/signal', () => {
    const err = new WorkerCrashedError('w-2');
    expect(err.exitCode).toBeUndefined();
    expect(err.signal).toBeUndefined();
  });

  it('WorkerUnhealthyError includes reason', () => {
    const err = new WorkerUnhealthyError('w-3', 'heartbeat timeout');
    expect(err.name).toBe('WorkerUnhealthyError');
    expect(err.code).toBe('WORKER_UNHEALTHY');
    expect(err.workerId).toBe('w-3');
    expect(err.reason).toBe('heartbeat timeout');
    expect(err.message).toContain('heartbeat timeout');
  });
});
