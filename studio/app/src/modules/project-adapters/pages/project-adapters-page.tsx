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
  UIInput,
  UIPage,
  UIPageHeader,
  UISpin,
  UITable,
  UITag,
  type UITableColumn,
} from '@kb-labs/studio-ui-kit';
import { useDataSources } from '@/providers/data-sources-provider';
import { useProjectAdapters, useInstallAndBindProjectAdapter } from '@kb-labs/studio-data-client';
import type { ProjectAdapterEntry } from '@kb-labs/studio-data-client';
import { ProjectPicker } from '@/components/project-picker';
import { AdapterConfigModal } from '../components/adapter-config-modal';

export function ProjectAdaptersPage() {
  const [projectId, setProjectId] = React.useState<string | null>(null);
  const [packageSpec, setPackageSpec] = React.useState('');
  const [slot, setSlot] = React.useState('');
  const { projectAdapters: source } = useDataSources();
  const { data: adapters, isLoading } = useProjectAdapters(source, projectId);
  const [editing, setEditing] = React.useState<ProjectAdapterEntry | null>(null);
  const installAndBind = useInstallAndBindProjectAdapter(source, projectId);

  const handleInstallAndBind = React.useCallback(async () => {
    try {
      const result = await installAndBind.mutateAsync({ spec: packageSpec.trim(), slot: slot.trim() });
      UIMessage.success(`Installed ${result.packageId} and bound it to ${result.slot}`);
      setPackageSpec('');
      setSlot('');
    } catch (error) {
      UIMessage.error(error instanceof Error ? error.message : 'Failed to install or bind the adapter');
    }
  }, [installAndBind, packageSpec, slot]);

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
        description="Install adapters at machine scope, then bind a slot for the selected project."
        actions={(
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <ProjectPicker value={projectId} onChange={setProjectId} />
            <UIInput aria-label="Adapter package and version" placeholder="@scope/adapter@1.2.3" value={packageSpec} onChange={(event) => setPackageSpec(event.target.value)} style={{ width: 230 }} />
            <UIInput aria-label="Adapter slot" placeholder="slot" value={slot} onChange={(event) => setSlot(event.target.value)} style={{ width: 100 }} />
            <UIButton variant="primary" disabled={!projectId || !packageSpec.trim() || !slot.trim()} loading={installAndBind.isPending} onClick={handleInstallAndBind}>
              Install and bind
            </UIButton>
          </div>
        )}
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
