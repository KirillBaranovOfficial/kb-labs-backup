import { describe, expect, it } from 'vitest';
import { EVENT_NAMES } from '@kb-labs/workflow-constants';
import type { IEventBus } from '@kb-labs/core-platform';
import { FaultInjectingCache, FaultInjectingEventBus, InMemoryCache, InMemoryEventBus, InMemoryStorage } from '@kb-labs/core-platform/inmemory';
import { mockLogger } from '@kb-labs/shared-testing';
import { EnvSecretProvider } from '../secrets.js';
import { WorkflowEngine } from '../engine.js';
import { workflowDeadLetterCacheKey } from '../dead-letter-key.js';
import { workflowProjectCacheKey } from '../project-scoped-cache.js';
import type { CacheOperation, EventBusOperation } from '@kb-labs/core-platform/inmemory';
import type { WorkflowRun } from '@kb-labs/workflow-contracts';

interface SideEffectObservation {
  readRun?: () => Promise<WorkflowRun | null>;
  status?: WorkflowRun['status'];
}

function failingEventBus(failType: string, error: Error, observation: SideEffectObservation): FaultInjectingEventBus {
  return new FaultInjectingEventBus(new InMemoryEventBus(), async (operation: EventBusOperation) => {
    const event = operation.event;
    if (event && typeof event === 'object' && 'type' in event && event.type === failType) {
      observation.status = (await observation.readRun?.())?.status;
      return error;
    }
    return undefined;
  });
}

async function terminalFailure(
  events: IEventBus = new InMemoryEventBus(),
  deadLetterError?: Error,
  observation: SideEffectObservation = {},
) {
  let deadLetterKey: string | undefined;
  const projectId = 'prj_0000000000000001';
  const cache = deadLetterError
    ? new FaultInjectingCache(new InMemoryCache(), async (operation: CacheOperation) => {
        if (operation.name === 'set' && operation.key === deadLetterKey) {
          observation.status = (await observation.readRun?.())?.status;
          return deadLetterError;
        }
        return undefined;
      })
    : new InMemoryCache();
  const logger = mockLogger();
  const engine = new WorkflowEngine({
    projectId,
    cache,
    storage: new InMemoryStorage(),
    events,
    logger,
    secretProvider: new EnvSecretProvider(),
  });
  const run = await engine.createRun({
    spec: {
      name: 'side-effect-failure',
      version: '1.0.0',
      on: { manual: true },
      jobs: {
        failed: { runsOn: 'local', steps: [{ name: 'Fail', uses: 'builtin:shell', with: { run: 'false' } }] },
        dependent: { runsOn: 'local', needs: ['failed'], steps: [{ name: 'Blocked', uses: 'builtin:shell', with: { run: 'true' } }] },
      },
    },
    trigger: { type: 'manual' },
  });
  const failedJob = run.jobs.find((job) => job.jobName === 'failed')!;
  await engine.markJobStarted(run.id, failedJob.id);
  deadLetterKey = workflowProjectCacheKey(projectId, workflowDeadLetterCacheKey(run.id, failedJob.id));
  observation.readRun = () => engine.getRun(run.id);
  await engine.markJobFailed(run.id, failedJob.id, new Error('step failed'), undefined, false);
  return { engine, logger, run, failedJob, observation };
}

async function successfulRun(events: FaultInjectingEventBus, observation: SideEffectObservation) {
  const logger = mockLogger();
  const engine = new WorkflowEngine({
    projectId: 'prj_0000000000000001',
    cache: new InMemoryCache(),
    storage: new InMemoryStorage(),
    events,
    logger,
    secretProvider: new EnvSecretProvider(),
  });
  const run = await engine.createRun({
    spec: {
      name: 'side-effect-success', version: '1.0.0', on: { manual: true },
      jobs: { success: { runsOn: 'local', steps: [{ name: 'Pass', uses: 'builtin:shell', with: { run: 'true' } }] } },
    },
    trigger: { type: 'manual' },
  });
  const job = run.jobs[0]!;
  await engine.markJobStarted(run.id, job.id);
  observation.readRun = () => engine.getRun(run.id);
  await engine.markJobCompleted(run.id, job.id);
  return { engine, logger, run, job, observation };
}

describe('workflow terminal state precedes ancillary effects', () => {
  // case: S-027.4
  it('records correlated publish failure after the failed run is durable', async () => {
    const error = new Error('event bus unavailable');
    const observation: SideEffectObservation = {};
    const { engine, logger, run, failedJob } = await terminalFailure(failingEventBus(EVENT_NAMES.job.failed, error, observation), undefined, observation);

    await expect(engine.getRun(run.id)).resolves.toMatchObject({
      status: 'failed',
      jobs: [expect.objectContaining({ id: failedJob.id, status: 'failed' }), expect.objectContaining({ status: 'cancelled' })],
    });
    expect(observation.status).toBe('failed');
    expect(logger.messages).toContainEqual(expect.objectContaining({
      level: 'error',
      msg: 'Workflow ancillary operation failed',
      error,
      meta: { operation: 'event.publish.job.failed', runId: run.id, jobId: failedJob.id },
    }));
  });

  it('records correlated dependent-cancellation notification failure after terminal state is durable', async () => {
    const error = new Error('cancellation notification unavailable');
    const observation: SideEffectObservation = {};
    const { engine, logger, run, failedJob } = await terminalFailure(failingEventBus(EVENT_NAMES.job.cancelled, error, observation), undefined, observation);
    const dependentJob = run.jobs.find((job) => job.jobName === 'dependent')!;

    await expect(engine.getRun(run.id)).resolves.toMatchObject({
      status: 'failed',
      jobs: [expect.objectContaining({ id: failedJob.id, status: 'failed' }), expect.objectContaining({ status: 'cancelled' })],
    });
    expect(observation.status).toBe('failed');
    expect(logger.messages).toContainEqual(expect.objectContaining({
      level: 'error',
      msg: 'Workflow ancillary operation failed',
      error,
      meta: { operation: 'event.publish.job.cancelled', runId: run.id, jobId: dependentJob.id },
    }));
  });

  it('records correlated dead-letter write failure after the failed run is durable', async () => {
    const error = new Error('cache unavailable');
    const observation: SideEffectObservation = {};
    const { engine, logger, run, failedJob } = await terminalFailure(new InMemoryEventBus(), error, observation);

    await expect(engine.getRun(run.id)).resolves.toMatchObject({
      status: 'failed',
      jobs: [expect.objectContaining({ id: failedJob.id, status: 'failed' }), expect.objectContaining({ status: 'cancelled' })],
    });
    expect(observation.status).toBe('failed');
    expect(logger.messages).toContainEqual(expect.objectContaining({
      level: 'error',
      msg: 'Workflow ancillary operation failed',
      error,
      meta: { operation: 'cache.dead-letter.set', runId: run.id, jobId: failedJob.id },
    }));
  });

  it('records correlated success-event failure after the successful run is durable', async () => {
    const error = new Error('event bus unavailable');
    const observation: SideEffectObservation = {};
    const { engine, logger, run, job } = await successfulRun(failingEventBus(EVENT_NAMES.run.finished, error, observation), observation);

    await expect(engine.getRun(run.id)).resolves.toMatchObject({
      status: 'success', jobs: [expect.objectContaining({ id: job.id, status: 'success' })],
    });
    expect(observation.status).toBe('success');
    expect(logger.messages).toContainEqual(expect.objectContaining({
      level: 'error', msg: 'Workflow ancillary operation failed', error,
      meta: { operation: 'event.publish.run.finished', runId: run.id, jobId: job.id },
    }));
  });
});
