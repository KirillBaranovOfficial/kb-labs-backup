import { afterAll, beforeAll, describe, expect, it } from 'vitest'
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import os from 'node:os'

import { loadPlatformConfig } from '../config-loader.js'

let tmpDir: string

/**
 * ADR-0047 layering in `loadPlatformConfig`:
 * generated (.kb/generated) < platform user config < project user config.
 */

function write(root: string, rel: string, contents: unknown): void {
  const full = path.join(root, rel)
  mkdirSync(path.dirname(full), { recursive: true })
  writeFileSync(full, typeof contents === 'string' ? contents : JSON.stringify(contents))
}

function makePlatformDir(dir: string): void {
  mkdirSync(path.join(dir, 'node_modules', '@kb-labs', 'cli-bin'), { recursive: true })
}

function writeProjectSettings(root: string, contents: unknown): void {
  const home = path.join(tmpDir, 'kb-home')
  const projectId = path.basename(root)
  const dir = path.join(home, 'state', projectId)
  mkdirSync(dir, { recursive: true })
  writeFileSync(path.join(dir, 'config.jsonc'), JSON.stringify(contents))
}

describe('loadPlatformConfig generated layer (ADR-0047)', () => {
  beforeAll(() => {
    tmpDir = mkdtempSync(path.join(os.tmpdir(), 'kb-cfg-gen-'))
  })

  afterAll(() => {
    rmSync(tmpDir, { recursive: true, force: true })
  })

  function load(platformRoot: string, projectRoot: string) {
    const projectId = path.basename(projectRoot)
    return loadPlatformConfig({
      startDir: projectRoot,
      env: { KB_PLATFORM_ROOT: platformRoot, KB_PROJECT_ROOT: projectRoot, KB_HOME: path.join(tmpDir, 'kb-home') },
      projectId,
      loadEnvFile: false,
    })
  }

  it('rejects project store adapter overrides above generated and platform settings', async () => {
    const platformRoot = path.join(tmpDir, 'a-platform')
    const projectRoot = path.join(tmpDir, 'a-project')
    makePlatformDir(platformRoot)
    write(platformRoot, '.kb/generated/topology.jsonc', `{
      // written by the installer
      "platform": {
        "adapters": { "llm": "generated-llm", "cache": "generated-cache", "storage": "generated-storage" },
        "adapterOptions": { "serviceTransport": { "services": { "rest": { "url": "http://127.0.0.1:5050" } } } }
      }
    }`)
    write(platformRoot, '.kb/kb.config.json', {
      platform: { adapters: { llm: 'platform-llm', cache: 'platform-cache' } },
    })
    writeProjectSettings(projectRoot, { platform: { adapters: { llm: 'project-llm' } } })

    await expect(load(platformRoot, projectRoot)).rejects.toMatchObject({ name: 'AdapterProjectScopeError' })
  })

  it('generated sections and project store settings are present in effectiveConfig', async () => {
    const platformRoot = path.join(tmpDir, 'b-platform')
    const projectRoot = path.join(tmpDir, 'b-project')
    makePlatformDir(platformRoot)
    write(platformRoot, '.kb/generated/gateway.json', { gateway: { port: 4000, host: '127.0.0.1' } })
    write(projectRoot, '.kb/kb.config.json', { gateway: { host: 'legacy-repository-value' } })
    writeProjectSettings(projectRoot, { plugins: { sample: { enabled: true } } })

    const result = await load(platformRoot, projectRoot)
    expect(result.effectiveConfig).toEqual({ gateway: { port: 4000, host: '127.0.0.1' }, plugins: { sample: { enabled: true } } })
    expect(result.rawConfig).toBeUndefined()
  })

  it('keeps working, with no generated source reported, when .kb/generated is absent', async () => {
    const platformRoot = path.join(tmpDir, 'c-platform')
    const projectRoot = path.join(tmpDir, 'c-project')
    makePlatformDir(platformRoot)
    write(projectRoot, '.kb/kb.config.json', {})

    const result = await load(platformRoot, projectRoot)
    expect(result.platformConfig.adapters).toEqual({})
    expect(result.sources.generated).toBeUndefined()
  })

  it('projects without adapter config inherit the installer filesystem storage default', async () => {
    const platformRoot = path.join(tmpDir, 'storage-default-platform')
    const projectRoot = path.join(tmpDir, 'storage-default-project')
    makePlatformDir(platformRoot)
    write(platformRoot, '.kb/generated/adapters.json', {
      platform: {
        adapters: {
          storage: '@kb-labs/adapters-fs@2.120.1',
          cache: '@kb-labs/adapters-state-broker@2.120.1',
        },
        adapterOptions: {
          storage: { baseDir: '.kb/data' },
          cache: { url: 'http://127.0.0.1:7777' },
        },
      },
    })
    write(projectRoot, '.kb/kb.config.json', {})

    const result = await load(platformRoot, projectRoot)

    expect(result.platformConfig.adapters?.storage).toBe('@kb-labs/adapters-fs@2.120.1')
    expect(result.platformConfig.adapters?.cache).toBe('@kb-labs/adapters-state-broker@2.120.1')
    expect(result.platformConfig.adapterOptions?.storage).toEqual({ baseDir: '.kb/data' })
    expect(result.platformConfig.adapterOptions?.cache).toEqual({ url: 'http://127.0.0.1:7777' })
  })

  it('a malformed generated file is skipped instead of breaking the load', async () => {
    const platformRoot = path.join(tmpDir, 'd-platform')
    const projectRoot = path.join(tmpDir, 'd-project')
    makePlatformDir(platformRoot)
    write(platformRoot, '.kb/generated/broken.json', '{ nope')
    write(projectRoot, '.kb/kb.config.json', {})

    const result = await load(platformRoot, projectRoot)
    expect(result.platformConfig.adapters).toEqual({})
    expect(result.sources.generated).toBeUndefined()
  })
})
