import {
  UITabs,
  UICard,
  UIPage,
  UIPageHeader,
} from '@kb-labs/studio-ui-kit';
import { AppearanceSettings } from '../components/appearance-settings';
import { DataPrivacySettings } from '../components/data-privacy-settings';
import { ExperimentalSettings } from '../components/experimental-settings';
import { NavigationSettings } from '../components/navigation-settings';

export function SettingsPage() {
  const tabItems = [
    {
      key: 'appearance',
      label: 'Appearance',
      children: (
        <UICard>
          <AppearanceSettings />
        </UICard>
      ),
    },
    {
      key: 'navigation',
      label: 'Navigation',
      children: (
        <UICard>
          <NavigationSettings />
        </UICard>
      ),
    },
    {
      key: 'privacy',
      label: 'Data & Privacy',
      children: (
        <UICard>
          <DataPrivacySettings />
        </UICard>
      ),
    },
    {
      key: 'experimental',
      label: 'Experimental',
      children: (
        <UICard>
          <ExperimentalSettings />
        </UICard>
      ),
    },
  ];

  return (
    <UIPage>
      <UIPageHeader
        title="Settings"
        description="Configure your preferences, manage data, and customize your experience"
        tabs={<UITabs items={tabItems} syncUrl={{ mode: 'path', basePath: '/settings' }} />}
      />
    </UIPage>
  );
}
