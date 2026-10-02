/**
 * Tests for PageErrorBoundary — verifies error boundary state machine.
 *
 * Since @testing-library/react is not available, we test the static method
 * and error handling logic directly without rendering.
 */
import { describe, it, expect, vi } from 'vitest';
import { PageErrorBoundary } from '../page-error-boundary.js';

describe('PageErrorBoundary', () => {
  describe('getDerivedStateFromError', () => {
    it('sets hasError=true and captures the error', () => {
      const error = new Error('plugin crashed');
      const state = PageErrorBoundary.getDerivedStateFromError(error);

      expect(state.hasError).toBe(true);
      expect(state.error).toBe(error);
    });

    it('captures different error types', () => {
      const typeError = new TypeError('cannot read property');
      const state = PageErrorBoundary.getDerivedStateFromError(typeError);

      expect(state.hasError).toBe(true);
      expect(state.error).toBeInstanceOf(TypeError);
      expect(state.error!.message).toBe('cannot read property');
    });
  });

  describe('handleRetry', () => {
    it('resets error state', () => {
      const boundary = new PageErrorBoundary({
        pageId: 'dashboard',
        pluginId: '@kb-labs/test',
        children: null,
      });

      // Simulate error
      boundary.state = { hasError: true, error: new Error('boom'), failure: null };

      // Create spy on setState
      const setStateSpy = vi.spyOn(boundary, 'setState');
      boundary.handleRetry();

      expect(setStateSpy).toHaveBeenCalledWith({ hasError: false, error: null, failure: null });
    });
  });

  describe('componentDidCatch', () => {
    it('calls onError callback when provided', () => {
      const onError = vi.fn();
      const boundary = new PageErrorBoundary({
        pageId: 'dashboard',
        pluginId: '@kb-labs/test',
        children: null,
        onError,
      });

      const error = new Error('crash');
      const errorInfo = { componentStack: 'at Plugin' } as any;

      // Suppress structured console diagnostics.
      const consoleSpy = vi.spyOn(console, 'debug').mockImplementation(() => {});
      boundary.componentDidCatch(error, errorInfo);
      consoleSpy.mockRestore();

      expect(onError).toHaveBeenCalledWith(error, errorInfo);
    });

    it('writes a structured correlated diagnostic without passing the raw error to console.error or console.warn', () => {
      const boundary = new PageErrorBoundary({
        pageId: 'settings',
        pluginId: '@kb-labs/auth',
        children: null,
      });

      const consoleSpy = vi.spyOn(console, 'debug').mockImplementation(() => {});
      const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
      const consoleWarn = vi.spyOn(console, 'warn').mockImplementation(() => {});

      boundary.componentDidCatch(new Error('boom'), { componentStack: '' } as any);

      expect(consoleSpy).toHaveBeenCalledTimes(1);
      expect(consoleSpy.mock.calls[0]).toHaveLength(1);
      const log = JSON.parse(String(consoleSpy.mock.calls[0]![0])) as Record<string, unknown>;
      expect(log.correlationId).toEqual(expect.any(String));
      expect(log.stack).toContain('boom');
      expect(consoleError).not.toHaveBeenCalled();
      expect(consoleWarn).not.toHaveBeenCalled();
      consoleSpy.mockRestore();
      consoleError.mockRestore();
      consoleWarn.mockRestore();
    });
  });
});
