import { describe, expect, it, vi } from 'vitest';
import { HttpClient } from '../../client/http-client';
import { HttpProjectPluginsSource } from '../http-project-plugins-source';

describe('HttpProjectPluginsSource', () => {
  it('installs a project plugin through the UX result route', async () => {
    const client = new HttpClient('/api/v1', 'user-token');
    const fetchUx = vi.spyOn(client, 'fetchUx').mockResolvedValueOnce({ installed: [] });
    const source = new HttpProjectPluginsSource(client);

    await source.installPlugin('project-a', '@kb-labs/commit@2.120.1');

    expect(fetchUx).toHaveBeenCalledWith('/plugins/install', {
      method: 'POST',
      data: { projectId: 'project-a', spec: '@kb-labs/commit@2.120.1' },
    });
  });
});
