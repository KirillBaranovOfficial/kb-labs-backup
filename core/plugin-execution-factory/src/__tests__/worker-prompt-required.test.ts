import { afterEach, describe, expect, it } from 'vitest';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { Worker } from '../backends/worker-pool/worker.js';
import type { ExecutionRequest } from '../types.js';

const promptWorkerScript = fileURLToPath(new URL('./fixtures/prompt-worker.mjs', import.meta.url));

function makeRequest(): ExecutionRequest {
  return {
    executionId: 'required-prompt-test',
    handlerRef: './dist/handler.js',
    pluginRoot: path.resolve('/tmp/plugin'),
    input: {},
    descriptor: {
      hostType: 'workflow',
      pluginId: '@kb-labs/test-prompt',
      handlerId: 'required-prompt',
    } as unknown as ExecutionRequest['descriptor'],
  };
}

describe('worker prompt relay', () => {
  let worker: Worker | undefined;

  afterEach(() => {
    worker?.kill();
    worker = undefined;
  });

  // case: S-215.1
  it('returns the prompt failure to the worker instead of its default value', async () => {
    worker = new Worker({ workerScript: promptWorkerScript });
    await worker.spawn();

    await expect(worker.execute(
      makeRequest(),
      2_000,
    )).rejects.toMatchObject({
      code: 'KB_RUNTIME_INPUT_INVALID',
      hint: 'Supply the value requested by "Continue?" as command or request input.',
      cause: 'The prompt "Continue?" has no responder in this execution context.',
    });
  });

  it('uses a caller-provided hint when the execution context has one', async () => {
    worker = new Worker({ workerScript: promptWorkerScript });
    await worker.spawn();

    await expect(worker.execute(
      makeRequest(),
      2_000,
      undefined,
      undefined,
      undefined,
      'Add the response under inputs.approval.',
    )).rejects.toMatchObject({
      code: 'KB_RUNTIME_INPUT_INVALID',
      hint: 'Add the response under inputs.approval.',
      cause: 'The prompt "Continue?" has no responder in this execution context.',
    });
  });
});
