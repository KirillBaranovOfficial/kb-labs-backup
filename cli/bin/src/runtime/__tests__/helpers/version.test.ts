/**
 * Unit tests for version helper
 */

import { describe, it, expect } from 'vitest';
import { resolveVersion } from '../../helpers/version';

describe('resolveVersion', () => {
  it('should return options version when provided', () => {
    expect(resolveVersion('1.2.3', {})).toBe('1.2.3');
  });

  it('should return env version when options not provided', () => {
    expect(resolveVersion(undefined, { KB_VERSION: '2.0.0' })).toBe('2.0.0');
  });

  it('uses required package metadata injected by the CLI binary', () => {
    expect(resolveVersion(undefined, { CLI_VERSION: '2.120.1' })).toBe('2.120.1');
  });

  it('should prefer options over env', () => {
    expect(resolveVersion('1.0.0', { KB_VERSION: '2.0.0' })).toBe('1.0.0');
  });

  it('fails when package metadata and explicit version are missing', () => {
    expect(() => resolveVersion(undefined, {})).toThrow(/CLI version metadata is required/);
  });

  it('fails when version metadata is empty', () => {
    expect(() => resolveVersion('', {})).toThrow(/CLI version metadata is required/);
  });

  it('does not treat empty environment metadata as a version', () => {
    expect(() => resolveVersion(undefined, { CLI_VERSION: '', KB_VERSION: '' })).toThrow(/CLI version metadata is required/);
  });
});
