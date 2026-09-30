import * as React from 'react';
import { UISelect, UIText } from '@kb-labs/studio-ui-kit';
import { useProjects } from '@kb-labs/studio-data-client';
import { useDataSources } from '@/providers/data-sources-provider';
import { useProjectContext } from '@/providers/project-context-provider';

export function ProjectContextSelector() {
  const { projects } = useDataSources();
  const { data, isLoading } = useProjects(projects);
  const { projectId, setProjectId } = useProjectContext();

  React.useEffect(() => {
    if (data?.length && (!projectId || !data.some((project) => project.id === projectId))) {
      setProjectId(data[0]!.id);
    }
  }, [data, projectId, setProjectId]);

  if (!isLoading && (!data || data.length === 0)) {
    return <UIText color="secondary">No registered project</UIText>;
  }

  return (
    <UISelect
      aria-label="Active project"
      value={projectId ?? undefined}
      loading={isLoading}
      placeholder="Select a project"
      style={{ minWidth: 260 }}
      options={(data ?? []).map((project) => ({ label: project.name, value: project.id }))}
      onChange={(next) => setProjectId(String(next))}
    />
  );
}
