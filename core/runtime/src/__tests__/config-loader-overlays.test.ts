import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest'
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import os from 'node:os'

import { AdapterProjectScopeError } from '@kb-labs/core-platform'
import { loadPlatformConfig } from '../config-loader.js'

let tmpDir: string

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
  const dir = path.join(tmpDir, 'kb-home', 'state', path.basename(projectRoot), 'overlays')
  mkdirSync(dir, { recursive: true })
  const full = path.join(dir, name)
  writeFileSync(full, contents)
  return full
}

function writeProjectSettings(projectRoot: string, contents: unknown): void {
  const dir = path.join(tmpDir, 'kb-home', 'state', path.basename(projectRoot))
  mkdirSync(dir, { recursive: true })
  writeFileSync(path.join(dir, 'config.jsonc'), JSON.stringify(contents))
}

function load(platformRoot: string, projectRoot: string) {
  return loadPlatformConfig({
    startDir: projectRoot,
    env: {
      KB_PLATFORM_ROOT: platformRoot,
      KB_PROJECT_ROOT: projectRoot,
      KB_HOME: path.join(tmpDir, 'kb-home'),
    },
    projectId: path.basename(projectRoot),
    loadEnvFile: false,
  })
}

describe('loadPlatformConfig: overlays', () => {
  let originalCwd: string

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

    const result = await load(platformRoot, projectRoot)

    expect(result.platformConfig.adapters).toEqual({ llm: 'openai' })
    expect(result.sources.overlays).toBeUndefined()
  })

  it('applies overlays to platform data while leaving adapter values platform-owned', async () => {
    const platformRoot = path.join(tmpDir, 'o2-platform')
    const projectRoot = path.join(tmpDir, 'o2-project')
    makePlatformDir(platformRoot, { platform: { adapters: { llm: 'openai' }, features: { rerank: false } } })
    makeProjectDir(projectRoot)
    const overlayPath = writeOverlay(projectRoot, 'pressure__overlay.jsonc', '{ "platform": { "features": { "rerank": true } } }')

    const result = await load(platformRoot, projectRoot)

    expect(result.platformConfig.adapters).toEqual({ llm: 'openai' })
    expect((result.platformConfig.platform as Record<string, unknown>)?.features).toEqual({ rerank: true })
    expect(result.sources.overlays).toEqual([overlayPath])
  })

  it('merges multiple overlays in lexicographic order', async () => {
    const platformRoot = path.join(tmpDir, 'o3-platform')
    const projectRoot = path.join(tmpDir, 'o3-project')
    makePlatformDir(platformRoot, { platform: { features: { mode: 'base', keep: true } } })
    makeProjectDir(projectRoot)
    const aPath = writeOverlay(projectRoot, 'a.jsonc', '{ "platform": { "features": { "mode": "first" } } }')
    const bPath = writeOverlay(projectRoot, 'b.jsonc', '{ "platform": { "features": { "mode": "last", "added": true } } }')

    const result = await load(platformRoot, projectRoot)

    expect((result.platformConfig.platform as Record<string, unknown>)?.features).toEqual({ mode: 'last', added: true })
    expect(result.sources.overlays).toEqual([aPath, bPath])
  })

  it('effectiveConfig applies overlays to product sections; rawConfig stays raw', async () => {
    const platformRoot = path.join(tmpDir, 'o-eff-platform')
    const projectRoot = path.join(tmpDir, 'o-eff-project')
    makePlatformDir(platformRoot)
    makeProjectDir(projectRoot)
    writeProjectSettings(projectRoot, { plugins: { mind: { retrieval: { rerank: true } } } })
    writeOverlay(projectRoot, 'hyde__overlay.jsonc', '{ "plugins": { "mind": { "retrieval": { "hyde": true } } } }')

    const result = await load(platformRoot, projectRoot)

    const effectivePlugins = result.effectiveConfig?.plugins as Record<string, { retrieval: Record<string, unknown> }> | undefined
    expect(effectivePlugins?.mind?.retrieval).toEqual({ rerank: true, hyde: true })
    expect(result.rawConfig).toBeUndefined()
  })

  it('rejects a project overlay that attempts to set adapters', async () => {
    const platformRoot = path.join(tmpDir, 'o4-platform')
    const projectRoot = path.join(tmpDir, 'o4-project')
    makePlatformDir(platformRoot)
    makeProjectDir(projectRoot)
    writeOverlay(projectRoot, 'adapter.jsonc', '{ "adapters": { "llm": "anthropic" } }')

    await expect(load(platformRoot, projectRoot)).rejects.toBeInstanceOf(AdapterProjectScopeError)
  })

  it('throws when an overlay breaks the platform config top-level type', async () => {
    const platformRoot = path.join(tmpDir, 'o5-platform')
    const projectRoot = path.join(tmpDir, 'o5-project')
    makePlatformDir(platformRoot)
    makeProjectDir(projectRoot)
    writeOverlay(projectRoot, 'bad.jsonc', '{ "execution": "not-an-object" }')

    await expect(load(platformRoot, projectRoot)).rejects.toThrow(/Platform config is invalid after applying overlays/)
  })

  it('propagates overlay loader diagnostics into sources.overlayDiagnostics', async () => {
    const platformRoot = path.join(tmpDir, 'o6-platform')
    const projectRoot = path.join(tmpDir, 'o6-project')
    makePlatformDir(platformRoot)
    makeProjectDir(projectRoot)
    writeOverlay(projectRoot, 'a-good.jsonc', '{ "platform": { "features": { "mode": "safe" } } }')
    writeOverlay(projectRoot, 'b-broken.jsonc', '{ this is not valid json')
    writeOverlay(projectRoot, 'c-array.jsonc', '[1, 2, 3]')

    const result = await load(platformRoot, projectRoot)

    expect(result.sources.overlayDiagnostics?.map((diagnostic) => diagnostic.code)).toEqual(expect.arrayContaining(['JSON_PARSE_FAILED', 'OVERLAY_NOT_OBJECT']))
  })

  it('omits overlayDiagnostics when there are no issues', async () => {
    const platformRoot = path.join(tmpDir, 'o7-platform')
    const projectRoot = path.join(tmpDir, 'o7-project')
    makePlatformDir(platformRoot)
    makeProjectDir(projectRoot)
    writeOverlay(projectRoot, 'p.jsonc', '{ "platform": { "features": { "mode": "safe" } } }')

    const result = await load(platformRoot, projectRoot)

    expect(result.sources.overlayDiagnostics).toBeUndefined()
  })
})
