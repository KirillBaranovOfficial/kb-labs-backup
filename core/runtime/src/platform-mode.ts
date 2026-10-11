export interface PlatformModeSource {
  sameLocation: boolean;
  rawConfig?: Record<string, unknown>;
  rawPlatformConfig?: Record<string, unknown>;
}

/** Resolve mode from the config file that owns the platform. */
export function isWorkspaceDevelopmentMode(config: PlatformModeSource): boolean {
  const source = config.sameLocation ? config.rawConfig : config.rawPlatformConfig;
  if (!source) return false;
  const platform = source.platform;
  return Boolean(platform && typeof platform === 'object' &&
    (platform as Record<string, unknown>).mode === 'development');
}
