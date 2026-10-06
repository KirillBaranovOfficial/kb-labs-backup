/**
 * @module @kb-labs/studio-app/modules/analytics/routes
 * Analytics module routing configuration
 */

import type { RouteObject } from 'react-router-dom';
import { AnalyticsOverviewPage } from '../pages/analytics-overview-page';
import { AnalyticsEventsPage } from '../pages/analytics-events-page';
import { AnalyticsLLMPage } from '../pages/analytics-llm-page';
import { AnalyticsEmbeddingsPage } from '../pages/analytics-embeddings-page';
import { ErrorBoundary } from '../../../components/error-boundary';
import { renderIcon } from '../../../routes/helpers';
import type { NavigationItem } from '@/components/ui';

// Route paths
const PATHS = {
  OVERVIEW: '/analytics/overview',
  EVENTS: '/analytics/events',
  LLM: '/analytics/llm',
  EMBEDDINGS: '/analytics/embeddings',
} as const;

// Route keys
const KEYS = {
  MODULE: 'analytics',
  OVERVIEW: 'analytics-overview',
  EVENTS: 'analytics-events',
  LLM: 'analytics-llm',
  EMBEDDINGS: 'analytics-embeddings',
} as const;

/**
 * Analytics routes
 */
export const analyticsRoutes: RouteObject[] = [
  {
    path: PATHS.OVERVIEW,
    element: <AnalyticsOverviewPage />,
    errorElement: <ErrorBoundary />,
  },
  {
    path: PATHS.EVENTS,
    element: <AnalyticsEventsPage />,
    errorElement: <ErrorBoundary />,
  },
  {
    path: PATHS.LLM,
    element: <AnalyticsLLMPage />,
    errorElement: <ErrorBoundary />,
  },
  {
    path: PATHS.EMBEDDINGS,
    element: <AnalyticsEmbeddingsPage />,
    errorElement: <ErrorBoundary />,
  },
];

/**
 * Analytics navigation item for sidebar
 */
export const analyticsNavigation: NavigationItem = {
  key: KEYS.MODULE,
  label: 'Analytics',
  icon: renderIcon('BarChartOutlined'),
  children: [
    {
      key: 'analytics-group-general',
      label: 'General',
      type: 'group',
      children: [
        {
          key: KEYS.OVERVIEW,
          label: 'Overview',
          path: PATHS.OVERVIEW,
          icon: renderIcon('DashboardOutlined'),
        },
        {
          key: KEYS.EVENTS,
          label: 'Events',
          path: PATHS.EVENTS,
          icon: renderIcon('ThunderboltOutlined'),
        },
      ],
    },
    {
      key: 'analytics-group-adapters',
      label: 'Adapters',
      type: 'group',
      children: [
        {
          key: KEYS.LLM,
          label: 'LLM Usage',
          path: PATHS.LLM,
          icon: renderIcon('RobotOutlined'),
        },
        {
          key: KEYS.EMBEDDINGS,
          label: 'Embeddings',
          path: PATHS.EMBEDDINGS,
          icon: renderIcon('NodeIndexOutlined'),
        },
      ],
    },
  ],
};
