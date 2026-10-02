import type { RouteResult } from '@kb-labs/cli-commands';

type GlobalFlags = { help?: boolean; version?: boolean };

export function resolveProjectScope(
  route: RouteResult,
  global: GlobalFlags,
  cmdPath: string[],
): 'platform' | 'required' {
  if (global.version || (global.help && cmdPath.length === 0)) {
    return 'platform';
  }
  if (route.type === 'not-found' || route.type === 'ambiguous') {
    return 'platform';
  }
  if (route.type === 'system-cmd') {
    return route.cmd.scope === 'platform' ? 'platform' : 'required';
  }
  return 'required';
}
