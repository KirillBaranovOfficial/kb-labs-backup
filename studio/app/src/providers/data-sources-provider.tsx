import { createContext, useContext, useEffect, useMemo, type ReactNode } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { createDataSources, type DataSources } from '@kb-labs/studio-data-client';
import { studioConfig } from '@/config/studio.config';
import { useProjectContext } from './project-context-provider';

// Exported so tests can inject a mock DataSources value via <DataSourcesContext.Provider>.
export const DataSourcesContext = createContext<DataSources | null>(null);

export function DataSourcesProvider({ children }: { children: ReactNode }) {
  const { projectId } = useProjectContext();
  const queryClient = useQueryClient();
  const sources = useMemo(
    () => createDataSources({
      mode: studioConfig.dataSourceMode,
      baseUrl: studioConfig.apiBaseUrl,
      token: studioConfig.gatewayToken,
      projectId: projectId ?? undefined,
    }),
    [projectId],
  );

  useEffect(() => {
    void queryClient.invalidateQueries({ queryKey: ['observability'] });
    void queryClient.invalidateQueries({ queryKey: ['analytics'] });
  }, [projectId, queryClient]);

  return <DataSourcesContext.Provider value={sources}>{children}</DataSourcesContext.Provider>;
}

export function useDataSources() {
  const context = useContext(DataSourcesContext);
  if (!context) {
    throw new Error('useDataSources must be used within DataSourcesProvider');
  }
  return context;
}
