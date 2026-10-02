import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest'
import { mkdtempSync, mkdirSync, realpathSync, rmSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import os from 'node:os'

import { loadPlatformConfig } from '../config-loader.js'
import { createProjectRegistry } from '@kb-labs/core-project-registry'

/**
 * Create a minimal platform installation layout at `<dir>`:
 *   <dir>/node_modules/@kb-labs/cli-bin/
 *   <dir>/.kb/kb.config.json (optional)
 */
function makePlatformDir(dir: string, configContents?: unknown): void {
  mkdirSync(path.join(dir, 'node_modules', '@kb-labs', 'cli-bin'), {
    recursive: true,
  })
  if (configContents !== undefined) {
    mkdirSync(path.join(dir, '.kb'), { recursive: true })
    writeFileSync(
      path.join(dir, '.kb', 'kb.config.json'),
      JSON.stringify(configContents),
    )
  }
}

/**
 * Create a minimal user project layout at `<dir>`:
 *   <dir>/.kb/kb.config.json (optional)
 */
function makeProjectDir(dir: string, configContents?: unknown): void {
  mkdirSync(dir, { recursive: true })
  if (configContents !== undefined) {
    mkdirSync(path.join(dir, '.kb'), { recursive: true })
    writeFileSync(
      path.join(dir, '.kb', 'kb.config.json'),
      JSON.stringify(configContents),
    )
  }
}

describe('loadPlatformConfig', () => {
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
    tmpDir = mkdtempSync(path.join(os.tmpdir(), 'kb-cfg-'))
  })

  afterEach(() => {
    process.chdir(tmpDir)
  })

  afterAll(() => {
    process.chdir(originalCwd)
    rmSync(tmpDir, { recursive: true, force: true })
  })

  it('rejects project adapter bindings even when platform has none', async () => {
    const platformRoot = path.join(tmpDir, 'p1-platform')
    const projectRoot = path.join(tmpDir, 'p1-project')
    makePlatformDir(platformRoot) // no config
    makeProjectDir(projectRoot, {
      platform: {
        adapters: { llm: 'noop' },
      },
    })

    await expect(loadConfig({
      startDir: projectRoot,
      env: {
        KB_PLATFORM_ROOT: platformRoot,
        KB_PROJECT_ROOT: projectRoot,
      },
      loadEnvFile: false,
    })).rejects.toMatchObject({ name: 'AdapterProjectScopeError' })
  })

  it('rejects legacy top-level project adapter sections with the typed scope error', async () => {
    const platformRoot = path.join(tmpDir, 'p1-legacy-platform')
    const projectRoot = path.join(tmpDir, 'p1-legacy-project')
    makePlatformDir(platformRoot)
    makeProjectDir(projectRoot, { adapters: { llm: ['openai'] } })

    await expect(loadConfig({
      startDir: projectRoot,
      env: { KB_PLATFORM_ROOT: platformRoot, KB_PROJECT_ROOT: projectRoot },
      loadEnvFile: false,
    })).rejects.toMatchObject({ name: 'AdapterProjectScopeError' })
  })

  it('rejects project adapter bindings and options with an actionable typed error', async () => {
    const platformRoot = path.join(tmpDir, 'p-platform-only')
    const projectRoot = path.join(tmpDir, 'p-project-only')
    makePlatformDir(platformRoot, { platform: { adapters: { llm: 'openai' } } })
    makeProjectDir(projectRoot, {
      platform: { adapterOptions: { llm: { model: 'project-model' } } },
    })

    await expect(loadConfig({
      startDir: projectRoot,
      env: { KB_PLATFORM_ROOT: platformRoot, KB_PROJECT_ROOT: projectRoot },
      loadEnvFile: false,
    })).rejects.toMatchObject({ name: 'AdapterProjectScopeError' })
  })

  it('loads only platform defaults when project has no config', async () => {
    const platformRoot = path.join(tmpDir, 'p2-platform')
    const projectRoot = path.join(tmpDir, 'p2-project')
    makePlatformDir(platformRoot, {
      platform: {
        adapters: { cache: 'redis' },
      },
    })
    makeProjectDir(projectRoot) // no config

    const result = await loadConfig({
      startDir: projectRoot,
      env: {
        KB_PLATFORM_ROOT: platformRoot,
        KB_PROJECT_ROOT: projectRoot,
      },
      loadEnvFile: false,
    })

    expect(result.platformConfig.adapters).toEqual({ cache: 'redis' })
    expect(result.sources.platformDefaults).toBe(
      realpathSync(path.join(platformRoot, '.kb', 'kb.config.json')),
    )
    expect(result.sources.projectConfig).toBeUndefined()
  })

  it('rejects project adapter bindings instead of merging them with platform config', async () => {
    const platformRoot = path.join(tmpDir, 'p3-platform')
    const projectRoot = path.join(tmpDir, 'p3-project')
    makePlatformDir(platformRoot, {
      platform: {
        adapters: {
          llm: 'openai',
          cache: 'redis',
        },
      },
    })
    makeProjectDir(projectRoot, {
      platform: {
        adapters: {
          llm: 'anthropic', // project-level adapter configuration is rejected
        },
      },
    })

    await expect(loadConfig({
      startDir: projectRoot,
      env: {
        KB_PLATFORM_ROOT: platformRoot,
        KB_PROJECT_ROOT: projectRoot,
      },
      loadEnvFile: false,
    })).rejects.toMatchObject({ name: 'AdapterProjectScopeError' })
  })

  it('returns empty-adapters base when neither layer exists', async () => {
    const platformRoot = path.join(tmpDir, 'p4-platform')
    const projectRoot = path.join(tmpDir, 'p4-project')
    makePlatformDir(platformRoot) // no config
    makeProjectDir(projectRoot) // no config

    const result = await loadConfig({
      startDir: projectRoot,
      env: {
        KB_PLATFORM_ROOT: platformRoot,
        KB_PROJECT_ROOT: projectRoot,
      },
      loadEnvFile: false,
    })

    // Base is { adapters: {} } so downstream code can always destructure it.
    expect(result.platformConfig).toEqual({ adapters: {} })
    expect(result.sources.platformDefaults).toBeUndefined()
    expect(result.sources.projectConfig).toBeUndefined()
  })

  it('reads single file once when platformRoot === projectRoot (dev mode)', async () => {
    const workspaceRoot = path.join(tmpDir, 'p5-workspace')
    makePlatformDir(workspaceRoot, {
      platform: {
        adapters: { llm: 'workspace-llm' },
      },
    })

    const result = await loadConfig({
      startDir: workspaceRoot,
      env: {
        KB_PLATFORM_ROOT: workspaceRoot,
        KB_PROJECT_ROOT: workspaceRoot,
      },
      loadEnvFile: false,
    })

    expect(result.sameLocation).toBe(true)
    expect(result.platformConfig.adapters).toEqual({ llm: 'workspace-llm' })
    // Only project config reported; platformDefaults left undefined so we
    // don't double-count the same file.
    expect(result.sources.platformDefaults).toBeUndefined()
    expect(result.sources.projectConfig).toBe(
      realpathSync(path.join(workspaceRoot, '.kb', 'kb.config.json')),
    )
  })

  it('reports no ignored fields when platformRoot === projectRoot, even for platform-only fields', async () => {
    // sameLocation: true is the monorepo/dogfooding case — one physical file
    // plays both roles, so the platform<->project merge is a no-op and
    // nothing can conflict. This guards against the merge policy accidentally
    // treating the single file's own platform-only fields (e.g. `execution`)
    // as a rejected project override of itself.
    const workspaceRoot = path.join(tmpDir, 'p5b-workspace')
    makePlatformDir(workspaceRoot, {
      platform: {
        adapters: { llm: 'workspace-llm' },
        execution: { mode: 'worker-pool' },
      },
    })

    const result = await loadConfig({
      startDir: workspaceRoot,
      env: {
        KB_PLATFORM_ROOT: workspaceRoot,
        KB_PROJECT_ROOT: workspaceRoot,
      },
      loadEnvFile: false,
    })

    expect(result.sameLocation).toBe(true)
    expect(result.sources.ignoredProjectFields).toBeUndefined()
    // Every resolved field is attributed to the single (platform) layer —
    // there's no separate project layer to merge against in this mode.
    expect(result.sources.fields).toEqual({
      adapters: 'platform',
      execution: 'platform',
    })
  })

  it('silently degrades when config file is malformed JSON', async () => {
    const platformRoot = path.join(tmpDir, 'p6-platform')
    const projectRoot = path.join(tmpDir, 'p6-project')
    makePlatformDir(platformRoot)
    mkdirSync(path.join(projectRoot, '.kb'), { recursive: true })
    writeFileSync(
      path.join(projectRoot, '.kb', 'kb.config.json'),
      '{ this is not: valid json',
    )

    const result = await loadConfig({
      startDir: projectRoot,
      env: {
        KB_PLATFORM_ROOT: platformRoot,
        KB_PROJECT_ROOT: projectRoot,
      },
      loadEnvFile: false,
    })

    // No throw, empty-adapters base, caller can still detect the failure
    // via sources (left undefined) or by inspecting the (missing) raw config.
    expect(result.platformConfig).toEqual({ adapters: {} })
  })

  it('tolerates config without a `platform` section', async () => {
    const platformRoot = path.join(tmpDir, 'p7-platform')
    const projectRoot = path.join(tmpDir, 'p7-project')
    makePlatformDir(platformRoot)
    makeProjectDir(projectRoot, { unrelated: 'data' })

    const result = await loadConfig({
      startDir: projectRoot,
      env: {
        KB_PLATFORM_ROOT: platformRoot,
        KB_PROJECT_ROOT: projectRoot,
      },
      loadEnvFile: false,
    })

    expect(result.platformConfig).toEqual({ adapters: {} })
    // rawConfig is preserved so callers can still inspect non-platform fields
    expect(result.rawConfig).toEqual({ unrelated: 'data' })
  })

  it('loads .env file from projectRoot when loadEnvFile is true', async () => {
    const platformRoot = path.join(tmpDir, 'p8-platform')
    const projectRoot = path.join(tmpDir, 'p8-project')
    makePlatformDir(platformRoot)
    makeProjectDir(projectRoot, { platform: {} })
    writeFileSync(
      path.join(projectRoot, '.env'),
      'KB_TEST_LOADED_VAR=from-env-file\n',
    )

    // Pre-condition
    delete process.env.KB_TEST_LOADED_VAR

    await loadConfig({
      startDir: projectRoot,
      env: {
        KB_PLATFORM_ROOT: platformRoot,
        KB_PROJECT_ROOT: projectRoot,
      },
      loadEnvFile: true,
    })

    expect(process.env.KB_TEST_LOADED_VAR).toBe('from-env-file')

    // Cleanup
    delete process.env.KB_TEST_LOADED_VAR
  })

  it('does not load .env when loadEnvFile is false', async () => {
    const platformRoot = path.join(tmpDir, 'p9-platform')
    const projectRoot = path.join(tmpDir, 'p9-project')
    makePlatformDir(platformRoot)
    makeProjectDir(projectRoot, { platform: {} })
    writeFileSync(
      path.join(projectRoot, '.env'),
      'KB_TEST_SHOULD_NOT_LOAD=nope\n',
    )

    delete process.env.KB_TEST_SHOULD_NOT_LOAD

    await loadConfig({
      startDir: projectRoot,
      env: {
        KB_PLATFORM_ROOT: platformRoot,
        KB_PROJECT_ROOT: projectRoot,
      },
      loadEnvFile: false,
    })

    expect(process.env.KB_TEST_SHOULD_NOT_LOAD).toBeUndefined()
  })

  it('throws on an unresolved ${VAR} placeholder when NODE_ENV=production', async () => {
    // A cloud deployment with a missing secret must crash at boot, not serve
    // traffic with a literal `${VAR}` baked into config (docs/adr/0037).
    const platformRoot = path.join(tmpDir, 'p10-platform')
    const projectRoot = path.join(tmpDir, 'p10-project')
    makePlatformDir(platformRoot, {
      platform: { adapters: { llm: '${MISSING_SECRET_VAR}' } },
    })
    makeProjectDir(projectRoot)

    delete process.env.MISSING_SECRET_VAR

    await expect(
      loadConfig({
        startDir: projectRoot,
        env: {
          KB_PLATFORM_ROOT: platformRoot,
          KB_PROJECT_ROOT: projectRoot,
          NODE_ENV: 'production',
        },
        loadEnvFile: false,
      }),
    ).rejects.toThrow(/MISSING_SECRET_VAR/)
  })

  it('leaves an unresolved ${VAR} placeholder intact outside production', async () => {
    const platformRoot = path.join(tmpDir, 'p11-platform')
    const projectRoot = path.join(tmpDir, 'p11-project')
    makePlatformDir(platformRoot, {
      platform: { adapters: { llm: '${MISSING_SECRET_VAR}' } },
    })
    makeProjectDir(projectRoot)

    delete process.env.MISSING_SECRET_VAR

    const result = await loadConfig({
      startDir: projectRoot,
      env: {
        KB_PLATFORM_ROOT: platformRoot,
        KB_PROJECT_ROOT: projectRoot,
      },
      loadEnvFile: false,
    })

    expect(result.platformConfig.adapters?.llm).toBe('${MISSING_SECRET_VAR}')
  })

  it('shifts loopback adapter infrastructure URLs with KB_NET_OFFSET', async () => {
    const workspaceRoot = path.join(tmpDir, 'p-net-offset-workspace')
    makePlatformDir(workspaceRoot, {
      platform: {
        adapters: { cache: 'redis', vectorStore: 'qdrant' },
        adapterOptions: {
          cache: { url: 'redis://localhost:6379' },
          vectorStore: { url: 'http://localhost:6333' },
          serviceTransport: {
            services: { workflow: { url: 'http://localhost:7778' } },
          },
        },
      },
    })

    const result = await loadConfig({
      startDir: workspaceRoot,
      env: {
        KB_PLATFORM_ROOT: workspaceRoot,
        KB_PROJECT_ROOT: workspaceRoot,
        KB_NET_OFFSET: '1000',
      },
      loadEnvFile: false,
    })

    expect(result.platformConfig.adapterOptions).toMatchObject({
      cache: { url: 'redis://localhost:7379' },
      vectorStore: { url: 'http://localhost:7333' },
      // The transport owns its own offset application, so it stays canonical
      // here and is shifted exactly once when the adapter is constructed.
      serviceTransport: {
        services: { workflow: { url: 'http://localhost:7778' } },
      },
    })
  })
})
