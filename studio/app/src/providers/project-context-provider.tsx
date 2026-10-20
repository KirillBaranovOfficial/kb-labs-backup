import { createContext, useContext, useMemo, useState, type ReactNode } from 'react';

const STORAGE_KEY = 'kb-studio-active-project';

interface ProjectContextValue {
  projectId: string | null;
  setProjectId: (projectId: string) => void;
}

const ProjectContext = createContext<ProjectContextValue | null>(null);

function readProjectId(): string | null {
  try {
    return window.localStorage.getItem(STORAGE_KEY);
  } catch {
    return null;
  }
}

export function ProjectContextProvider({ children }: { children: ReactNode }) {
  const [projectId, updateProjectId] = useState<string | null>(readProjectId);
  const value = useMemo<ProjectContextValue>(() => ({
    projectId,
    setProjectId: (nextProjectId) => {
      updateProjectId(nextProjectId);
      try {
        window.localStorage.setItem(STORAGE_KEY, nextProjectId);
      } catch {
        // The in-memory selection still works when browser storage is disabled.
      }
    },
  }), [projectId]);

  return <ProjectContext.Provider value={value}>{children}</ProjectContext.Provider>;
}

export function useProjectContext(): ProjectContextValue {
  const context = useContext(ProjectContext);
  if (!context) throw new Error('useProjectContext must be used within ProjectContextProvider');
  return context;
}
