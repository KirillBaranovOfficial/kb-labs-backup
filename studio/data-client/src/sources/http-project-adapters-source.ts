/**
 * HTTP implementation of ProjectAdaptersDataSource, over the host's
 * `/api/v1/adapters` API.
 */

import type { HttpClient } from '../client/http-client';
import type {
  ConfigureAdapterResult,
  ProjectAdapterEntry,
  ProjectAdaptersDataSource,
} from './project-adapters-source';

export class HttpProjectAdaptersSource implements ProjectAdaptersDataSource {
  constructor(private readonly client: HttpClient) {}

  async listAdapters(projectId: string): Promise<ProjectAdapterEntry[]> {
    return this.client.fetch<ProjectAdapterEntry[]>('/adapters', {
      params: { projectId },
    });
  }

  async configureAdapter(
    projectId: string,
    name: string,
    options: Record<string, unknown>,
  ): Promise<ConfigureAdapterResult> {
    return this.client.fetch(`/adapters/${encodeURIComponent(name)}/configure`, {
      method: 'POST',
      data: { projectId, options },
    });
  }

  async installAdapter(spec: string): Promise<{ id: string; version: string }> {
    const result = await this.client.fetch<{ installed: Array<{ id: string; version: string; primaryKind: string }> }>(
      '/marketplace/packages/install',
      { method: 'POST', data: { specs: [spec], scope: 'platform' } },
    );
    const entry = result.installed[0];
    if (!entry || entry.primaryKind !== 'adapter') {
      if (entry) {
        await this.client.fetch('/marketplace/packages/uninstall', {
          method: 'POST', data: { packageIds: [entry.id], scope: 'platform' },
        });
      }
      throw new Error(`Package ${spec} is not an adapter; no platform package was kept.`);
    }
    return { id: entry.id, version: entry.version };
  }

  async bindAdapter(projectId: string, slot: string, packageId: string): Promise<{ slot: string; packageId: string; file: string; changed: boolean }> {
    return this.client.fetch<{ slot: string; packageId: string; file: string; changed: boolean }>('/adapters/bind', {
      method: 'POST', data: { projectId, slot, packageId },
    });
  }
}
