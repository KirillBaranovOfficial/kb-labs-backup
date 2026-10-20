/**
 * @module @kb-labs/core-runtime/run-executor
 * Bridge between run orchestration and execution backend.
 */

import type { ExecutionBackend, ExecutionRequest, NormalizedExecutionResult } from '@kb-labs/plugin-execution-factory';

/**
 * Request for run step execution.
 */
export interface RunStepExecutionRequest {
  runId: string;
  stepId: string;
  environmentId?: string;
  execution: ExecutionRequest;
}

/**
 * Run executor that enriches execution metadata with run/environment context.
 */
export class RunExecutor {
  constructor(
    private readonly executionBackend: ExecutionBackend,
    private readonly logger: { debug(message: string, meta?: Record<string, unknown>): void }
  ) {}

  /**
   * Execute one run step through the shared execution backend.
   */
  async executeStep(request: RunStepExecutionRequest): Promise<NormalizedExecutionResult> {
    const { runId, stepId, environmentId, execution } = request;

    this.logger.debug('RunExecutor: executing step', {
      runId,
      stepId,
      environmentId,
      executionId: execution.executionId,
      pluginRoot: execution.pluginRoot,
      handlerRef: execution.handlerRef,
    });

    return this.executionBackend.execute({
      ...execution,
      context: {
        ...execution.context,
        run: {
          runId,
          stepId,
          environmentId,
        },
      },
    });
  }
}
