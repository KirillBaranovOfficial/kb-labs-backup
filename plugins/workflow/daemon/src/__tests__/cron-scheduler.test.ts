import { describe, expect, it, vi } from 'vitest';
import { NoOpLogger } from '@kb-labs/core-platform/noop';
import type { WorkflowEngine } from '@kb-labs/workflow-engine';
import type { UserCron } from '@kb-labs/workflow-contracts';
import type { JobBroker } from '../job-broker.js';
import { CronScheduler } from '../cron-scheduler.js';

describe('CronScheduler project handler targets', () => {
  it('submits the handler and input with project cron metadata', async () => {
    const submit = vi.fn(async () => ({ id: 'job-run-1' }));
    const scheduler = new CronScheduler({
      jobBroker: { submit } as unknown as JobBroker,
      workflowEngine: {} as WorkflowEngine,
      logger: new NoOpLogger(),
    });
    scheduler.registerUserCron({
      id: 'review', schedule: '0 2 * * *', handler: 'steward:daily-review',
      input: { channel: 'team' }, enabled: true,
    } as UserCron);

    await scheduler.triggerNow('review');

    expect(submit).toHaveBeenCalledWith(expect.objectContaining({
      handler: 'steward:daily-review', input: { channel: 'team' },
      metadata: expect.objectContaining({ cronId: 'review', scheduledBy: 'cron' }),
    }));
    expect(scheduler.getJob('review')?.lastRun).toMatchObject({ id: 'job-run-1', status: 'queued' });
  });
});
