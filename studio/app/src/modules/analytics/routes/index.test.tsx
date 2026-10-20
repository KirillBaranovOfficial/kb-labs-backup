import { describe, expect, it } from 'vitest';
import { analyticsNavigation, analyticsRoutes } from './index.js';

describe('analytics routes', () => {
  it('does not expose retired adapter telemetry views', () => {
    const retiredPaths = ['/analytics/cache', '/analytics/storage', '/analytics/vectorstore'];
    const routePaths = analyticsRoutes.flatMap((route) => route.path ? [route.path] : []);
    const navigationPaths = analyticsNavigation.children?.flatMap((group) =>
      group.children?.flatMap((item) => item.path ? [item.path] : []) ?? [],
    ) ?? [];

    for (const path of retiredPaths) {
      expect(routePaths).not.toContain(path);
      expect(navigationPaths).not.toContain(path);
    }
  });
});
