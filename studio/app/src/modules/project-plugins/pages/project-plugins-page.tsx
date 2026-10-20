/**
 * @module @kb-labs/studio-app/modules/project-plugins/pages/project-plugins-page
 *
 * Per-project plugin enable/disable (stage 6, second slice). Studio only
 * calls the host's `/api/v1/plugins` API — it never touches
 * `.kb/marketplace.lock` itself.
 */

import * as React from 'react';
import {
  UIEmptyState,
  UIButton,
  UIInput,
  UIMessage,
  UIPage,
  UIPageHeader,
  UIAlert,
  UISpin,
  UISwitch,
  UITable,
  UITag,
  type UITableColumn,
} from '@kb-labs/studio-ui-kit';
import { useDataSources } from '@/providers/data-sources-provider';
import { useProjectPlugins, useSetProjectPluginEnabled, useInstallProjectPlugin, useUninstallProjectPlugin } from '@kb-labs/studio-data-client';
import type { ProjectPluginEntry } from '@kb-labs/studio-data-client';
import { KBError } from '@kb-labs/studio-data-client';
import { useProjectContext } from '@/providers/project-context-provider';

export function ProjectPluginsPage() {
  const { projectId } = useProjectContext();
  const [spec, setSpec] = React.useState('');
  const { projectPlugins: source } = useDataSources();
  const { data: plugins, isLoading } = useProjectPlugins(source, projectId);
  const setEnabled = useSetProjectPluginEnabled(source, projectId);
  const installPlugin = useInstallProjectPlugin(source, projectId);
  const uninstallPlugin = useUninstallProjectPlugin(source, projectId);
  const [installError, setInstallError] = React.useState<{ message: string; hint?: string } | null>(null);

  const handleToggle = React.useCallback(
    async (plugin: ProjectPluginEntry, enabled: boolean) => {
      try {
        await setEnabled.mutateAsync({ packageId: plugin.id, enabled });
        UIMessage.success(`${enabled ? 'Enabled' : 'Disabled'} ${plugin.id}`);
      } catch (error) {
        UIMessage.error(error instanceof Error ? error.message : 'Failed to update the plugin');
      }
    },
    [setEnabled],
  );

  const handleInstall = React.useCallback(async () => {
    const requested = spec.trim();
    if (!projectId || !requested) {return;}
    try {
      setInstallError(null);
      await installPlugin.mutateAsync(requested);
      setSpec('');
      UIMessage.success(`Installed ${requested} for this project`);
    } catch (error) {
      setInstallError({
        message: error instanceof KBError ? error.message : 'Could not install the requested plugin.',
        ...(error instanceof KBError && error.hint ? { hint: error.hint } : {}),
      });
    }
  }, [installPlugin, projectId, spec]);

  const handleUninstall = React.useCallback(async (packageId: string) => {
    try {
      await uninstallPlugin.mutateAsync(packageId);
      UIMessage.success(`Uninstalled ${packageId} from this project`);
    } catch (error) {
      UIMessage.error(error instanceof Error ? error.message : 'Failed to uninstall the plugin');
    }
  }, [uninstallPlugin]);

  const columns: UITableColumn<ProjectPluginEntry>[] = [
    {
      key: 'id',
      title: 'Plugin',
      dataIndex: 'id',
      render: (_value, plugin) => (
        <div>
          <div>{plugin.id}</div>
          <div style={{ opacity: 0.6, fontSize: 12 }}>{plugin.version}</div>
        </div>
      ),
    },
    {
      key: 'source',
      title: 'Source',
      dataIndex: 'source',
      render: (_value, plugin) => <UITag>{plugin.source}</UITag>,
    },
    {
      key: 'scope',
      title: 'Scope',
      dataIndex: 'scope',
    },
    {
      key: 'enabled',
      title: 'Enabled',
      width: 100,
      render: (_value, plugin) => (
        <UISwitch
          checked={plugin.enabled}
          loading={setEnabled.isPending}
          onChange={(checked) => handleToggle(plugin, checked)}
        />
      ),
    },
    {
      key: 'actions',
      title: '',
      width: 120,
      render: (_value, plugin) => (
        <UIButton variant="text" danger loading={uninstallPlugin.isPending} onClick={() => handleUninstall(plugin.id)}>
          Uninstall
        </UIButton>
      ),
    },
  ];

  return (
    <UIPage>
      <UIPageHeader
        title="Plugins"
        description="Enable or disable plugins for the selected project."
        actions={(
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <UIInput
              aria-label="Plugin package and version"
              placeholder="@scope/plugin@1.2.3"
              value={spec}
              onChange={setSpec}
              style={{ width: 240 }}
            />
            <UIButton variant="primary" disabled={!projectId || !spec.trim()} loading={installPlugin.isPending} onClick={handleInstall}>
              Install
            </UIButton>
          </div>
        )}
      />
      {installError && (
        <UIAlert
          variant="error"
          message={installError.message}
          description={installError.hint}
          showIcon
          style={{ marginBottom: 16 }}
        />
      )}
      {!projectId ? null : isLoading ? (
        <UISpin />
      ) : !plugins || plugins.length === 0 ? (
        <UIEmptyState
          title="No plugins found"
          description="This project has no plugins in its marketplace.lock yet."
        />
      ) : (
        <UITable<ProjectPluginEntry> columns={columns} dataSource={plugins} rowKey="id" pagination={false} />
      )}
    </UIPage>
  );
}
