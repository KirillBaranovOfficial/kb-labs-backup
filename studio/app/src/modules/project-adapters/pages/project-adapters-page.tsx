/**
 * @module @kb-labs/studio-app/modules/project-adapters/pages/project-adapters-page
 *
 * Per-project adapter list + config form (stage 6, second slice). Studio only
 * calls the host's `/api/v1/adapters` API — it never edits `kb.config.jsonc`
 * itself.
 */

import * as React from 'react';
import {
  UIButton,
  UIEmptyState,
  UIPage,
  UIPageHeader,
  UISpin,
  UITable,
  UITag,
  type UITableColumn,
} from '@kb-labs/studio-ui-kit';
import { useDataSources } from '@/providers/data-sources-provider';
import { useProjectAdapters } from '@kb-labs/studio-data-client';
import type { ProjectAdapterEntry } from '@kb-labs/studio-data-client';
import { useProjectContext } from '@/providers/project-context-provider';
import { AdapterConfigModal } from '../components/adapter-config-modal';

export function ProjectAdaptersPage() {
  const { projectId } = useProjectContext();
  const { projectAdapters: source } = useDataSources();
  const { data: adapters, isLoading } = useProjectAdapters(source, projectId);
  const [editing, setEditing] = React.useState<ProjectAdapterEntry | null>(null);

  const columns: UITableColumn<ProjectAdapterEntry>[] = [
    {
      key: 'name',
      title: 'Slot',
      dataIndex: 'name',
    },
    {
      key: 'spec',
      title: 'Package',
      dataIndex: 'spec',
      render: (_value, adapter) => (adapter.spec ? <UITag>{adapter.spec}</UITag> : '-'),
    },
    {
      key: 'options',
      title: 'Options',
      render: (_value, adapter) => Object.keys(adapter.options).length,
    },
    {
      key: 'actions',
      title: '',
      width: 140,
      render: (_value, adapter) => (
        <UIButton variant="text" onClick={() => setEditing(adapter)}>
          Configure
        </UIButton>
      ),
    },
  ];

  return (
    <UIPage>
      <UIPageHeader
        title="Adapters"
        description="Configure the adapters bound to the selected project."
      />
      {!projectId ? null : isLoading ? (
        <UISpin />
      ) : !adapters || adapters.length === 0 ? (
        <UIEmptyState title="No adapters configured" description="This project has no adapters bound yet." />
      ) : (
        <UITable<ProjectAdapterEntry> columns={columns} dataSource={adapters} rowKey="name" pagination={false} />
      )}
      <AdapterConfigModal projectId={projectId} adapter={editing} onClose={() => setEditing(null)} />
    </UIPage>
  );
}
