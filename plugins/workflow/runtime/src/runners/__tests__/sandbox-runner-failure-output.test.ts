import { describe, expect, it, vi } from 'vitest'
import type { IEntityRegistry } from '@kb-labs/core-registry'
import type { ExecutionBackend } from '@kb-labs/plugin-execution'
import { SandboxRunner } from '../sandbox-runner.js'
import type { StepExecutionRequest } from '../../types.js'

describe('SandboxRunner failed shell output', () => {
  async function executeFailedShell() {
    const backend = {
      execute: vi.fn().mockResolvedValue({
        ok: true,
        data: {
          ok: false,
          exitCode: 23,
          stdout: 'journey-failure-diagnostic\n',
          stderr: '',
        },
      }),
    } as unknown as ExecutionBackend
    const runner = new SandboxRunner({
      backend,
      cliApi: { resolve: vi.fn(), list: vi.fn() } as unknown as IEntityRegistry,
      workspaceRoot: '/project',
    })
    const request: StepExecutionRequest = {
      spec: { name: 'Deliberate failure', uses: 'builtin:shell', with: { command: 'echo failure' } },
      context: {
        runId: 'run-1',
        jobId: 'run-1:smoke',
        stepId: 'deliberate-failure',
        attempt: 1,
        env: {},
        secrets: {},
        logger: { debug: vi.fn(), info: vi.fn(), warn: vi.fn(), error: vi.fn() },
      },
      workspace: '/project',
    }

    return runner.execute(request)
  }

  // case: S-029.2
  it('includes captured command output in the failed step diagnostic', async () => {
    const result = await executeFailedShell()
    expect(result.status).toBe('failed')
    if (result.status !== 'failed') { throw new Error('expected a failed step result') }
    expect(result.error.message).toContain('journey-failure-diagnostic')
  })

  // case: S-029.3
  it('retains failed command output for workflow logs', async () => {
    const result = await executeFailedShell()
    expect(result.status).toBe('failed')
    if (result.status !== 'failed') { throw new Error('expected a failed step result') }
    expect(result.outputs).toEqual({
      stdout: 'journey-failure-diagnostic\n',
      stderr: '',
      exitCode: 23,
    })
  })
})
