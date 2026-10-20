import { UIAlert, UICard, UIPage, UIPageHeader, UISpin, UITag } from '@kb-labs/studio-ui-kit';
import { useHealthStatus, useReadyStatus } from '@kb-labs/studio-data-client';
import { useDataSources } from '@/providers/data-sources-provider';

export function DashboardPage() {
  const { system } = useDataSources();
  const health = useHealthStatus(system);
  const ready = useReadyStatus(system);
  const status = health.data?.snapshot?.status ?? (health.data?.ok ? 'healthy' : 'unknown');
  const healthy = status === 'healthy' && ready.data?.ready !== false;

  return (
    <UIPage>
      <UIPageHeader title="Dashboard" description="Current gateway health and readiness." />
      <UICard title="Gateway health" style={{ marginTop: 16 }}>
        {health.isLoading || ready.isLoading ? (
          <UISpin />
        ) : health.error || ready.error ? (
          <UIAlert
            variant="error"
            showIcon
            message="Gateway health is unavailable"
            description="Source: gateway health/readiness. Run kb-create status --platform-root <platform-root> or kb-create doctor --platform-root <platform-root>."
          />
        ) : (
          <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
            <UITag variant={healthy ? 'success' : 'warning'}>{healthy ? 'Healthy' : status}</UITag>
            <span>{ready.data?.ready ? 'Gateway is ready.' : 'Gateway is running but not ready.'}</span>
          </div>
        )}
      </UICard>
    </UIPage>
  );
}
