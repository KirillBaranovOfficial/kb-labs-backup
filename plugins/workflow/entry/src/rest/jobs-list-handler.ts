/**
 * REST handler: GET /workflows/jobs
 * Proxies to workflow daemon GET /api/v1/jobs
 */

import { defineHandler, rethrowForRest, type RestInput, type PluginContextV3 } from '@kb-labs/sdk';
import { fetchWorkflowApi, getWorkflowDaemonUrl } from '../http-client';
import type { JobStatusInfo } from '@kb-labs/workflow-contracts';

interface JobsListQuery {
  type?: string;
  status?: string;
  limit?: string;
  offset?: string;
}

interface JobListResponse {
  jobs: JobStatusInfo[];
}

interface ApiEnvelope<T> {
  ok: boolean;
  data?: T;
  error?: string;
}

export default defineHandler({
  async execute(
    ctx: PluginContextV3,
    input: RestInput<JobsListQuery>
  ): Promise<JobListResponse> {
    const daemonUrl = getWorkflowDaemonUrl();
    const { type, status, limit, offset } = input.query || {};
    const validStatuses = ['queued', 'running', 'success', 'failed', 'cancelled', 'skipped', 'dlq'];
    if (status && !validStatuses.includes(status)) {
      throw Object.assign(new Error(`Invalid job status: ${status}`), { statusCode: 400, code: 'KB_RUNTIME_INPUT_INVALID' });
    }

    // Build query string
    const params = new URLSearchParams();
    if (type) {params.set('type', type);}
    if (status) {params.set('status', status);}
    if (limit) {params.set('limit', limit);}
    if (offset) {params.set('offset', offset);}

    const queryString = params.toString();
    const url = `${daemonUrl}/jobs${queryString ? `?${queryString}` : ''}`;

    ctx.platform.logger.info(`[jobs-list-handler] Fetching jobs from ${url}`);

    try {
      const response = await fetchWorkflowApi(url, {
        method: 'GET',
        headers: {
          'Content-Type': 'application/json',
        },
      });

      if (!response.ok) {
        const errorData = await response.json().catch(() => ({ error: 'Unknown error' })) as { error?: string };
        ctx.platform.logger.error(`[jobs-list-handler] Failed to fetch jobs: ${JSON.stringify(errorData)}`);
        throw new Error(errorData.error || 'Failed to fetch jobs list');
      }

      const payload = (await response.json()) as ApiEnvelope<JobListResponse>;
      if (!payload.ok || !payload.data) {
        throw new Error(payload.error || 'Failed to fetch jobs list');
      }
      const data = payload.data;
      ctx.platform.logger.info(`[jobs-list-handler] Fetched ${data.jobs.length} jobs`);
      return data;
    } catch (error) {
      ctx.platform.logger.error(
        '[jobs-list-handler] Error fetching jobs list',
        error instanceof Error ? error : undefined
      );
      rethrowForRest(error);
    }
  },
});
