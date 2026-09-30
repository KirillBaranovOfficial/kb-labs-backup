/**
 * Mock implementation of ProjectAdaptersDataSource, for Studio's mock
 * data-source mode.
 */

import { KBError, errorCodes } from '../errors/kb-error';
import type {
  ConfigureAdapterResult,
  ProjectAdapterEntry,
  ProjectAdaptersDataSource,
} from '../sources/project-adapters-source';

export class MockProjectAdaptersSource implements ProjectAdaptersDataSource {
  private readonly byProject = new Map<string, ProjectAdapterEntry[]>();

  private entriesFor(projectId: string): ProjectAdapterEntry[] {
    if (!this.byProject.has(projectId)) {
      this.byProject.set(projectId, [
        {
          name: 'llm',
          spec: '@kb-labs/adapters-openai',
          configSchema: {
            apiKey: { type: 'string' },
            defaultModel: { type: 'string', enum: ['gpt-4o', 'gpt-4o-mini'] },
          },
          options: { defaultModel: 'gpt-4o-mini', apiKey: '***REDACTED***' },
        },
        {
          name: 'cache',
          spec: '@kb-labs/adapters-fs-cache',
          configSchema: { ttlSeconds: { type: 'number' } },
          options: { ttlSeconds: 3600 },
        },
      ]);
    }
    return this.byProject.get(projectId)!;
  }

  async listAdapters(projectId: string): Promise<ProjectAdapterEntry[]> {
    return [...this.entriesFor(projectId)];
  }

  async configureAdapter(
    projectId: string,
    name: string,
    options: Record<string, unknown>,
  ): Promise<ConfigureAdapterResult> {
    const entries = this.entriesFor(projectId);
    const found = entries.find((entry) => entry.name === name);
    if (!found) {
      throw new KBError(errorCodes.NOT_FOUND, `No adapter "${name}" configured for this project.`, 404);
    }
    found.options = { ...found.options, ...options };
    return {
      name,
      file: `/workspace/${projectId}/.kb/kb.config.jsonc`,
      changed: true,
      options: found.options,
    };
  }

  async installAdapter(spec: string): Promise<{ id: string; version: string }> {
    const split = spec.lastIndexOf('@');
    return { id: split > 0 ? spec.slice(0, split) : spec, version: split > 0 ? spec.slice(split + 1) : '1.0.0' };
  }

  async bindAdapter(projectId: string, slot: string, packageId: string) {
    const entries = this.entriesFor(projectId);
    const existing = entries.find((entry) => entry.name === slot);
    if (existing) { existing.spec = packageId; }
    else { entries.push({ name: slot, spec: packageId, options: {} }); }
    return { slot, packageId, file: `/workspace/${projectId}/.kb/kb.config.jsonc`, changed: true };
  }
}
