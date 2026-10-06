import { describe, expect, it } from 'vitest';
import { describeHandlerAccessFailure } from '../handler-access.js';

describe('describeHandlerAccessFailure', () => {
  // case: S-203.18
  it('distinguishes a missing handler from a handler that cannot be accessed', () => {
    const missing = new Error('file not found');
    Object.defineProperty(missing, 'code', { value: 'ENOENT' });
    expect(describeHandlerAccessFailure(missing)).toEqual({
      code: 'COMMAND_HANDLER_MISSING',
      reason: 'was not found',
    });

    const denied = new Error('permission denied');
    Object.defineProperty(denied, 'code', { value: 'EACCES' });
    expect(describeHandlerAccessFailure(denied)).toEqual({
      code: 'COMMAND_HANDLER_UNREADABLE',
      reason: 'could not be accessed: permission denied',
    });
  });
});
