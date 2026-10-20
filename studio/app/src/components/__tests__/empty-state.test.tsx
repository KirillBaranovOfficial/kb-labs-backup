// @vitest-environment jsdom
import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { EmptyState, type EmptyStateKind } from '../empty-state.js';

describe('Studio empty state categories', () => {
  // case: S-203.5
  it('exposes empty, loading, failed, unconfigured, and unavailable states distinctly', () => {
    for (const kind of ['empty', 'loading', 'failed', 'unconfigured', 'unavailable'] satisfies EmptyStateKind[]) {
      const { container, unmount } = render(
        <EmptyState kind={kind} title={`${kind} title`} description={`${kind} description`} />,
      );

      expect(container.firstElementChild?.getAttribute('data-state')).toBe(kind);
      expect(screen.getByText(`${kind} title`)).toBeTruthy();
      expect(screen.getByText(`${kind} description`)).toBeTruthy();
      unmount();
    }
  });
});
