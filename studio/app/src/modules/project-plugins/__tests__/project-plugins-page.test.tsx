/**
 * @vitest-environment jsdom
 *
 * ProjectPluginsPage (stage 6, second slice): uses the active project, lists its
 * plugins, and toggles enable/disable — all through the injected
 * ProjectPluginsDataSource, never touching `.kb/marketplace.lock` directly.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent, act } from '@testing-library/react';
import React from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { DataSources, ProjectPluginEntry } from '@kb-labs/studio-data-client';
import { DataSourcesContext } from '../../../providers/data-sources-provider';
import { ProjectContextProvider } from '../../../providers/project-context-provider';
import { ProjectPluginsPage } from '../pages/project-plugins-page';

function makePlugin(overrides: Partial<ProjectPluginEntry> = {}): ProjectPluginEntry {
  return {
    id: '@kb-labs/commit-entry',
    version: '1.0.0',
    source: 'workspace',
    primaryKind: 'plugin',
    provides: ['plugin'],
    enabled: true,
    scope: 'project',
    ...overrides,
  };
}

function renderPage(sources: Partial<DataSources>) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <DataSourcesContext.Provider value={sources as DataSources}>
        <ProjectContextProvider><ProjectPluginsPage /></ProjectContextProvider>
      </DataSourcesContext.Provider>
    </QueryClientProvider>,
  );
}

describe('ProjectPluginsPage', () => {
  beforeEach(() => {
    localStorage.setItem('kb-studio-active-project', 'prj_1');
    vi.stubGlobal('matchMedia', vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    })));
  });

  it('uses the active project and lists its plugins', async () => {
    const listPlugins = vi.fn().mockResolvedValue([makePlugin({ id: '@kb-labs/review-entry', enabled: false })]);
    renderPage({
      projectPlugins: { listPlugins } as never,
    });

    await waitFor(() => expect(screen.getByText('@kb-labs/review-entry')).toBeDefined());
    expect(listPlugins).toHaveBeenCalledWith('prj_1');
  });

  it('toggles a plugin on via the switch', async () => {
    const listPlugins = vi.fn().mockResolvedValue([makePlugin({ enabled: false })]);
    const enablePlugin = vi.fn().mockResolvedValue({ id: '@kb-labs/commit-entry', enabled: true });
    renderPage({
      projectPlugins: { listPlugins, enablePlugin } as never,
    });

    await waitFor(() => expect(screen.getByText('@kb-labs/commit-entry')).toBeDefined());

    await act(async () => {
      fireEvent.click(screen.getByRole('switch'));
    });

    await waitFor(() => expect(enablePlugin).toHaveBeenCalledWith('prj_1', '@kb-labs/commit-entry'));
  });
});
