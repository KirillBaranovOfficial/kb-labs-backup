import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest'
import { mkdtempSync, mkdirSync, realpathSync, rmSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import os from 'node:os'

import { AdapterProjectScopeError, PlatformConfigError } from '@kb-labs/core-platform'
import { createProjectRegistry } from '@kb-labs/core-project-registry'
import { loadPlatformConfig } from '../config-loader.js'

function makePlatformDir(dir: string, configContents?: unknown): void {
  mkdirSync(path.join(dir, 'node_modules', '@kb-labs', 'cli-bin'), { recursive: true })
  if (configContents !== undefined) {
    mkdirSync(path.join(dir, '.kb'), { recursive: true })
    writeFileSync(path.join(dir, '.kb', 'kb.config.json'), JSON.stringify(configContents))
  }
}

function makeProjectDir(dir: string, configContents: unknown = {}): void {
  mkdirSync(path.join(dir, '.kb'), { recursive: true })
  writeFileSync(path.join(dir, '.kb', 'kb.config.json'), JSON.stringify(configContents))
}

function writeOverlay(projectRoot: string, name: string, contents: string): string {
  const dir = path.join(projectRoot, '.kb', 'overlays')
  mkdirSync(dir, { recursive: true })
  const full = path.join(dir, name)
  writeFileSync(full, contents)
  return full
}

describe('loadPlatformConfig: overlays', () => {
  let tmpDir: string
  let originalCwd: string

  async function loadConfig(options: Parameters<typeof loadPlatformConfig>[0]) {
    const env: NodeJS.ProcessEnv = { ...options?.env, KB_HOME: path.join(tmpDir, 'kb-home') }
    if (env.KB_PLATFORM_ROOT) env.KB_PLATFORM_ROOT = realpathSync(env.KB_PLATFORM_ROOT)
    const startDir = realpathSync(options?.startDir ?? process.cwd())
    await createProjectRegistry({ env }).add(startDir)
    return loadPlatformConfig({ ...options, startDir, env })
  }

  beforeAll(() => {
    originalCwd = process.cwd()
    tmpDir = mkdtempSync(path.join(os.tmpdir(), 'kb-cfg-overlays-'))
  })

  afterEach(() => process.chdir(tmpDir))

  afterAll(() => {
    process.chdir(originalCwd)
    rmSync(tmpDir, { recursive: true, force: true })
  })

  it('returns sources.overlays undefined when no overlays directory exists', async () => {
    const platformRoot = path.join(tmpDir, 'o1-platform')
    const projectRoot = path.join(tmpDir, 'o1-project')
    makePlatformDir(platformRoot, { platform: { adapters: { llm: 'openai' } } })
    makeProjectDir(projectRoot)

    const result = await loadConfig({
      startDir: projectRoot,
      env: { KB_PLATFORM_ROOT: platformRoot, KB_PROJECT_ROOT: projectRoot },
      loadEnvFile: false,
    })

    expect(result.platformConfig.adapters).toEqual({ llm: 'openai' })
    expect(result.sources.overlays).toBeUndefined()
  })

  it('applies overlays to platform data while leaving adapter values platform-owned', async () => {
    const platformRoot = path.join(tmpDir, 'o2-platform')
    const projectRoot = path.join(tmpDir, 'o2-project')
    makePlatformDir(platformRoot, { platform: { adapters: { llm: 'openai' }, features: { rerank: false } } })
    makeProjectDir(projectRoot)
    const overlayPath = writeOverlay(projectRoot, 'pressure__overlay.jsonc', '{ "platform": { "features": { "rerank": true } } }')

    const result = await loadConfig({
      startDir: projectRoot,
      env: { KB_PLATFORM_ROOT: platformRoot, KB_PROJECT_ROOT: projectRoot },
      loadEnvFile: false,
    })

    expect(result.platformConfig.adapters).toEqual({ llm: 'openai' })
    expect((result.platformConfig.platform as Record<string, unknown>)?.features).toEqual({ rerank: true })
    expect(result.sources.overlays).toEqual([realpathSync(overlayPath)])
  })

  it('merges multiple overlays in lexicographic order', async () => {
    const platformRoot = path.join(tmpDir, 'o3-platform')
    const projectRoot = path.join(tmpDir, 'o3-project')
    makePlatformDir(platformRoot, { platform: { features: { mode: 'base', keep: true } } })
    makeProjectDir(projectRoot)
    const aPath = writeOverlay(projectRoot, 'a.jsonc', '{ "platform": { "features": { "mode": "first" } } }')
    const bPath = writeOverlay(projectRoot, 'b.jsonc', '{ "platform": { "features": { "mode": "last", "added": true } } }')

    const result = await loadConfig({
      startDir: projectRoot,
      env: { KB_PLATFORM_ROOT: platformRoot, KB_PROJECT_ROOT: projectRoot },
      loadEnvFile: false,
    })

    expect((result.platformConfig.platform as Record<string, unknown>)?.features).toEqual({ mode: 'last', added: true })
    expect(result.sources.overlays).toEqual([realpathSync(aPath), realpathSync(bPath)])
  })

  it('effectiveConfig applies overlays to product sections; rawConfig stays raw', async () => {
    const platformRoot = path.join(tmpDir, 'o-eff-platform')
    const projectRoot = path.join(tmpDir, 'o-eff-project')
    makePlatformDir(platformRoot)
    makeProjectDir(projectRoot, { mind: { retrieval: { rerank: true } } })
    writeOverlay(projectRoot, 'hyde__overlay.jsonc', '{ "mind": { "retrieval": { "hyde": true } } }')

    const result = await loadConfig({
      startDir: projectRoot,
      env: { KB_PLATFORM_ROOT: platformRoot, KB_PROJECT_ROOT: projectRoot },
      loadEnvFile: false,
    })

    expect((result.effectiveConfig?.mind as { retrieval: Record<string, unknown> }).retrieval).toEqual({ rerank: true, hyde: true })
    expect((result.rawConfig?.mind as { retrieval: Record<string, unknown> }).retrieval).toEqual({ rerank: true })
  })

  it('rejects a project overlay that attempts to set adapters', async () => {
    const platformRoot = path.join(tmpDir, 'o4-platform')
    const projectRoot = path.join(tmpDir, 'o4-project')
    makePlatformDir(platformRoot)
    makeProjectDir(projectRoot)
    writeOverlay(projectRoot, 'adapter.jsonc', '{ "adapters": { "llm": "anthropic" } }')

    await expect(loadConfig({
      startDir: projectRoot,
      env: { KB_PLATFORM_ROOT: platformRoot, KB_PROJECT_ROOT: projectRoot },
      loadEnvFile: false,
    })).rejects.toBeInstanceOf(AdapterProjectScopeError)
  })

  it('throws when an overlay breaks the platform config top-level type', async () => {
    const platformRoot = path.join(tmpDir, 'o5-platform')
    const projectRoot = path.join(tmpDir, 'o5-project')
    makePlatformDir(platformRoot)
    makeProjectDir(projectRoot)
    writeOverlay(projectRoot, 'bad.jsonc', '{ "execution": "not-an-object" }')

    await expect(loadConfig({
      startDir: projectRoot,
      env: { KB_PLATFORM_ROOT: platformRoot, KB_PROJECT_ROOT: projectRoot },
      loadEnvFile: false,
    })).rejects.toThrow(/Platform config is invalid after applying overlays/)
  })

  it('rejects a non-object overlay config file', async () => {
    const platformRoot = path.join(tmpDir, 'o6-platform')
    const projectRoot = path.join(tmpDir, 'o6-project')
    makePlatformDir(platformRoot)
    makeProjectDir(projectRoot)
    writeOverlay(projectRoot, 'a-good.jsonc', '{ "platform": { "features": { "mode": "safe" } } }')
    writeOverlay(projectRoot, 'c-array.jsonc', '[1, 2, 3]')

    await expect(loadConfig({
      startDir: projectRoot,
      env: { KB_PLATFORM_ROOT: platformRoot, KB_PROJECT_ROOT: projectRoot },
      loadEnvFile: false,
    })).rejects.toMatchObject({ name: 'PlatformConfigError', reason: 'invalid' })
  })

  it('omits overlayDiagnostics when there are no issues', async () => {
    const platformRoot = path.join(tmpDir, 'o7-platform')
    const projectRoot = path.join(tmpDir, 'o7-project')
    makePlatformDir(platformRoot)
    makeProjectDir(projectRoot)
    writeOverlay(projectRoot, 'p.jsonc', '{ "platform": { "features": { "mode": "safe" } } }')

    const result = await loadConfig({
      startDir: projectRoot,
      env: { KB_PLATFORM_ROOT: platformRoot, KB_PROJECT_ROOT: projectRoot },
      loadEnvFile: false,
    })

    expect(result.sources.overlayDiagnostics).toBeUndefined()
  })
})
