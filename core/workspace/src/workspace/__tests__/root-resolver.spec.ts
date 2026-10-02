import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest'
import { mkdtempSync, mkdirSync, realpathSync, rmSync, writeFileSync } from 'node:fs'
import { pathToFileURL } from 'node:url'
import path from 'node:path'
import os from 'node:os'
import { createProjectRegistry } from '@kb-labs/core-project-registry'

import {
  resolvePlatformRoot,
  resolveProjectRoot,
  resolveRoots,
  resolveWorkspaceRoot,
} from '../root-resolver'

describe('resolveProjectRoot', () => {
  let tmpDir: string
  let originalCwd: string

  beforeAll(() => {
    originalCwd = process.cwd()
    tmpDir = mkdtempSync(path.join(os.tmpdir(), 'kb-project-'))
  })

  afterEach(() => {
    process.chdir(tmpDir)
  })

  afterAll(() => {
    process.chdir(originalCwd)
    rmSync(tmpDir, { recursive: true, force: true })
  })

  // case: S-204.7
  it('reports an unknown project outside the platform registry', async () => {
    const unregistered = path.join(tmpDir, 'unregistered-project')
    const registryHome = path.join(tmpDir, 'registry-home')
    mkdirSync(unregistered, { recursive: true })
    await expect(resolveProjectRoot({
      startDir: unregistered,
      env: { KB_HOME: registryHome },
    })).rejects.toMatchObject({ code: 'KB_PROJECT_UNKNOWN' })
  })

  it('resolves the registered project containing cwd without reading repository config', async () => {
    const project = path.join(tmpDir, 'registered-project')
    const nested = path.join(project, 'packages', 'foo')
    const home = path.join(tmpDir, 'registered-home')
    mkdirSync(nested, { recursive: true })
    await createProjectRegistry({ root: home }).add(project)

    const result = await resolveProjectRoot({ startDir: nested, env: { KB_HOME: home } })
    expect(result).toEqual({ rootDir: realpathSync(project), source: 'registry' })
  })

  it('ignores .kb and repository markers for project identity', async () => {
    const project = path.join(tmpDir, 'unregistered-marked-project')
    mkdirSync(path.join(project, '.kb'), { recursive: true })
    writeFileSync(path.join(project, '.kb', 'kb.config.json'), '{}')
    writeFileSync(path.join(project, 'package.json'), '{"name":"unregistered"}')

    await expect(resolveProjectRoot({ startDir: project, env: { KB_HOME: path.join(tmpDir, 'empty-home') } }))
      .rejects.toMatchObject({ code: 'KB_PROJECT_UNKNOWN' })
  })
})

describe('resolveWorkspaceRoot (deprecated alias)', () => {
  it('behaves identically to resolveProjectRoot', async () => {
    const home = mkdtempSync(path.join(os.tmpdir(), 'kb-alias-home-'))
    const project = mkdtempSync(path.join(os.tmpdir(), 'kb-alias-project-'))
    await createProjectRegistry({ root: home }).add(project)
    const options = { cwd: project, env: { KB_HOME: home } }
    const legacy = await resolveWorkspaceRoot(options)
    const current = await resolveProjectRoot(options)
    expect(legacy).toEqual(current)
    rmSync(home, { recursive: true, force: true })
    rmSync(project, { recursive: true, force: true })
  })
})

describe('resolvePlatformRoot', () => {
  let tmpDir: string
  let originalCwd: string

  beforeAll(() => {
    originalCwd = process.cwd()
    tmpDir = mkdtempSync(path.join(os.tmpdir(), 'kb-platform-'))
  })

  afterEach(() => {
    process.chdir(tmpDir)
  })

  afterAll(() => {
    process.chdir(originalCwd)
    rmSync(tmpDir, { recursive: true, force: true })
  })

  it('returns explicit cwd when provided', async () => {
    const explicit = path.join(tmpDir, 'explicit')
    const result = await resolvePlatformRoot({ cwd: explicit })

    expect(result).toEqual({
      rootDir: path.resolve(explicit),
      source: 'explicit',
    })
  })

  it('prefers KB_PLATFORM_ROOT env var', async () => {
    const envRoot = path.join(tmpDir, 'env-platform')
    const result = await resolvePlatformRoot({
      env: { KB_PLATFORM_ROOT: envRoot },
    })

    expect(result).toEqual({
      rootDir: path.resolve(envRoot),
      source: 'env',
    })
  })

  it('resolves platform root via moduleUrl when node_modules/@kb-labs/cli-bin is present', async () => {
    // Simulate installed layout:
    //   <platformRoot>/node_modules/@kb-labs/cli-bin/dist/bin.js
    const platformRoot = path.join(tmpDir, 'installed-platform')
    const cliBinDist = path.join(
      platformRoot,
      'node_modules',
      '@kb-labs',
      'cli-bin',
      'dist',
    )
    mkdirSync(cliBinDist, { recursive: true })
    const fakeBin = path.join(cliBinDist, 'bin.js')
    writeFileSync(fakeBin, '// fake')

    const result = await resolvePlatformRoot({
      moduleUrl: pathToFileURL(fakeBin).href,
      startDir: tmpDir,
      env: {},
    })

    expect(result.rootDir).toEqual(path.resolve(platformRoot))
    expect(result.source).toBe('module')
  })

  it('walks up from startDir to find node_modules/@kb-labs/cli-bin', async () => {
    const platformRoot = path.join(tmpDir, 'marker-platform')
    const nested = path.join(platformRoot, 'some', 'deep', 'dir')
    mkdirSync(nested, { recursive: true })
    mkdirSync(
      path.join(platformRoot, 'node_modules', '@kb-labs', 'cli-bin'),
      { recursive: true },
    )

    const result = await resolvePlatformRoot({
      startDir: nested,
      env: {},
    })

    expect(result.rootDir).toEqual(path.resolve(platformRoot))
    expect(result.source).toBe('marker')
  })

  it('walks up from startDir to find pnpm-workspace.yaml (dev mode)', async () => {
    const workspaceRoot = path.join(tmpDir, 'dev-workspace')
    const nested = path.join(workspaceRoot, 'packages', 'foo')
    mkdirSync(nested, { recursive: true })
    writeFileSync(path.join(workspaceRoot, 'pnpm-workspace.yaml'), 'packages:\n  - packages/*\n')

    const result = await resolvePlatformRoot({
      startDir: nested,
      env: {},
    })

    expect(result.rootDir).toEqual(path.resolve(workspaceRoot))
    expect(result.source).toBe('marker')
  })

  it('prefers pnpm-workspace.yaml above a nested package marker (dev-mode correction)', async () => {
    // Simulates a pnpm monorepo where an individual package has its own
    // node_modules/@kb-labs/cli-bin symlink (via hoisting). We want the
    // workspace root, not the first package that happens to have the marker.
    const workspaceRoot = path.join(tmpDir, 'dev-pnpm-ws')
    const nestedPackage = path.join(
      workspaceRoot,
      'platform',
      'kb-labs-cli',
      'packages',
      'cli-bin',
    )
    const cliBinDist = path.join(nestedPackage, 'dist')
    mkdirSync(cliBinDist, { recursive: true })
    const fakeBin = path.join(cliBinDist, 'bin.js')
    writeFileSync(fakeBin, '// fake')

    // Nested package hoisting symlink (pnpm-style)
    mkdirSync(
      path.join(nestedPackage, 'node_modules', '@kb-labs', 'cli-bin'),
      { recursive: true },
    )
    // Workspace marker at the top
    writeFileSync(
      path.join(workspaceRoot, 'pnpm-workspace.yaml'),
      'packages:\n  - platform/*/packages/*\n',
    )

    const result = await resolvePlatformRoot({
      moduleUrl: pathToFileURL(fakeBin).href,
      startDir: tmpDir,
      env: {},
    })

    // Must return the workspace root, not the nested package.
    expect(result.rootDir).toEqual(path.resolve(workspaceRoot))
    expect(result.source).toBe('module')
  })

  it('prefers top-most pnpm-workspace.yaml in nested-workspace layouts', async () => {
    // KB Labs "workspace of workspaces" pattern: each sub-repo has its own
    // pnpm-workspace.yaml, but the outermost one is the true monorepo root.
    const topLevelWorkspace = path.join(tmpDir, 'kb-nested-root')
    const subRepoWorkspace = path.join(topLevelWorkspace, 'platform', 'kb-labs-cli')
    const cliBin = path.join(subRepoWorkspace, 'packages', 'cli-bin')
    const cliBinDist = path.join(cliBin, 'dist')
    mkdirSync(cliBinDist, { recursive: true })
    const fakeBin = path.join(cliBinDist, 'bin.js')
    writeFileSync(fakeBin, '// fake')

    // Top-level workspace file
    writeFileSync(
      path.join(topLevelWorkspace, 'pnpm-workspace.yaml'),
      'packages:\n  - platform/*/packages/*\n',
    )
    // Nested (sub-repo) workspace file — must be ignored in favor of the top.
    writeFileSync(
      path.join(subRepoWorkspace, 'pnpm-workspace.yaml'),
      'packages:\n  - packages/*\n',
    )

    const result = await resolvePlatformRoot({
      moduleUrl: pathToFileURL(fakeBin).href,
      startDir: tmpDir,
      env: {},
    })

    expect(result.rootDir).toEqual(path.resolve(topLevelWorkspace))
    expect(result.source).toBe('module')
  })

  it('ignores platform markers inside a pnpm virtual store', async () => {
    // Reproduces the installed-mode bug where pnpm resolves an import through
    // its virtual store (`node_modules/.pnpm/<pkg>@<ver>_<hash>/node_modules/<pkg>/`)
    // and walk-up finds a `node_modules/@kb-labs/cli-bin` marker *inside* the
    // store. That marker must be skipped — the real platform root is the
    // directory that owns the hoisted symlinks at the outer `node_modules/`.
    const platformRoot = path.join(tmpDir, 'pnpm-installed-platform')
    // Real hoisted symlink target (the outer layer we want to find).
    mkdirSync(
      path.join(platformRoot, 'node_modules', '@kb-labs', 'cli-bin'),
      { recursive: true },
    )
    // Virtual store entry that bin.js is physically resolved through.
    const storeCliBinDist = path.join(
      platformRoot,
      'node_modules',
      '.pnpm',
      '@kb-labs+cli-bin@2.5.0_hash',
      'node_modules',
      '@kb-labs',
      'cli-bin',
      'dist',
    )
    mkdirSync(storeCliBinDist, { recursive: true })
    // Also create a fake nested `node_modules/@kb-labs/cli-bin` to prove the
    // walker would have stopped there without the fix.
    mkdirSync(
      path.join(
        platformRoot,
        'node_modules',
        '.pnpm',
        '@kb-labs+cli-bin@2.5.0_hash',
        'node_modules',
        '@kb-labs',
        'cli-bin',
        'node_modules',
        '@kb-labs',
        'cli-bin',
      ),
      { recursive: true },
    )
    const fakeBin = path.join(storeCliBinDist, 'bin.js')
    writeFileSync(fakeBin, '// fake')

    const result = await resolvePlatformRoot({
      moduleUrl: pathToFileURL(fakeBin).href,
      startDir: tmpDir,
      env: {},
    })

    // Must return the outer platform root, NOT any directory inside .pnpm.
    expect(result.rootDir).toEqual(path.resolve(platformRoot))
    expect(result.source).toBe('module')
  })

  it('falls back to repo root when no markers match', async () => {
    const repoRoot = path.join(tmpDir, 'platform-repo-fallback')
    const nested = path.join(repoRoot, 'deep')
    mkdirSync(nested, { recursive: true })
    writeFileSync(path.join(repoRoot, 'package.json'), '{"name":"x"}')

    const result = await resolvePlatformRoot({
      startDir: nested,
      env: {},
    })

    expect(result.rootDir).toEqual(path.resolve(repoRoot))
    expect(result.source).toBe('repo')
  })
})

describe('resolveRoots', () => {
  let tmpDir: string
  let originalCwd: string

  beforeAll(() => {
    originalCwd = process.cwd()
    tmpDir = mkdtempSync(path.join(os.tmpdir(), 'kb-roots-'))
  })

  afterEach(() => {
    process.chdir(tmpDir)
  })

  afterAll(() => {
    process.chdir(originalCwd)
    rmSync(tmpDir, { recursive: true, force: true })
  })

  it('resolves platform roots without requiring a registered project', async () => {
    const platformRoot = path.join(tmpDir, 'platform-only-root')
    const cwd = path.join(tmpDir, 'unregistered-cwd')
    mkdirSync(cwd, { recursive: true })
    mkdirSync(path.join(platformRoot, 'node_modules', '@kb-labs', 'core-runtime'), { recursive: true })

    const result = await resolveRoots({
      startDir: cwd,
      env: { KB_PLATFORM_ROOT: platformRoot, KB_HOME: path.join(tmpDir, 'empty-home') },
      projectScope: 'platform',
    })

    expect(result).toEqual({
      platformRoot: path.resolve(platformRoot),
      projectRoot: path.resolve(platformRoot),
      sameLocation: true,
      sources: { platform: 'env', project: 'platform' },
    })
  })

  it('returns sameLocation: true in dev-mode workspace (both resolve to same root)', async () => {
    const workspaceRoot = path.join(tmpDir, 'dev-mode-workspace')
    const nested = path.join(workspaceRoot, 'packages', 'foo')
    const configDir = path.join(workspaceRoot, '.kb')

    mkdirSync(nested, { recursive: true })
    mkdirSync(configDir, { recursive: true })
    writeFileSync(
      path.join(configDir, 'kb.config.json'),
      JSON.stringify({ platform: {} }),
    )
    writeFileSync(
      path.join(workspaceRoot, 'pnpm-workspace.yaml'),
      'packages:\n  - packages/*\n',
    )

    const home = path.join(tmpDir, 'dev-registry-home')
    await createProjectRegistry({ root: home }).add(workspaceRoot)
    const result = await resolveRoots({
      startDir: nested,
      env: { KB_HOME: home },
    })

    expect(result.platformRoot).toEqual(path.resolve(workspaceRoot))
    expect(result.projectRoot).toEqual(realpathSync(workspaceRoot))
    expect(result.sameLocation).toBe(true)
    expect(result.sources.platform).toBe('marker')
    expect(result.sources.project).toBe('registry')
  })

  it('returns sameLocation: false in installed mode', async () => {
    // Platform installation
    const platformRoot = path.join(tmpDir, 'installed-platform-2')
    const cliBinDist = path.join(
      platformRoot,
      'node_modules',
      '@kb-labs',
      'cli-bin',
      'dist',
    )
    mkdirSync(cliBinDist, { recursive: true })
    const fakeBin = path.join(cliBinDist, 'bin.js')
    writeFileSync(fakeBin, '// fake')

    // User project
    const projectRoot = path.join(tmpDir, 'user-project')
    mkdirSync(projectRoot, { recursive: true })
    const home = path.join(tmpDir, 'installed-registry-home')
    await createProjectRegistry({ root: home }).add(projectRoot)

    const result = await resolveRoots({
      moduleUrl: pathToFileURL(fakeBin).href,
      startDir: projectRoot,
      env: { KB_HOME: home },
    })

    expect(result.platformRoot).toEqual(path.resolve(platformRoot))
    expect(result.projectRoot).toEqual(realpathSync(projectRoot))
    expect(result.sameLocation).toBe(false)
    expect(result.sources.platform).toBe('module')
    expect(result.sources.project).toBe('registry')
  })

  it('platform env override does not replace registry project identity', async () => {
    const platformRoot = path.join(tmpDir, 'env-platform-root')
    const projectRoot = path.join(tmpDir, 'env-project-root')
    mkdirSync(projectRoot, { recursive: true })

    const home = path.join(tmpDir, 'roots-registry-home')
    await createProjectRegistry({ root: home }).add(projectRoot)
    const result = await resolveRoots({
      startDir: projectRoot,
      env: {
        KB_PLATFORM_ROOT: platformRoot,
        KB_HOME: home,
      },
    })

    expect(result.platformRoot).toEqual(path.resolve(platformRoot))
    expect(result.projectRoot).toEqual(realpathSync(projectRoot))
    expect(result.sameLocation).toBe(false)
    expect(result.sources.platform).toBe('env')
    expect(result.sources.project).toBe('registry')
  })
})
