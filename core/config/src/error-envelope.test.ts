import { describe, expect, it } from 'vitest';
import { ConfigWriteError } from './user-config/write-user-config.js';
import { configErrorEnvelope } from './error-envelope.js';

describe('config error envelope', () => {
  // case: S-204.12
  it('explains a project-scope path error and gives its corrected command', () => {
    const envelope = configErrorEnvelope(new ConfigWriteError(
      'KB_CONFIG_INVALID',
      'Project scope accepts plugin settings under plugins.<configSection>.<key>',
      {
        path: 'commit.llm.enabled',
        reason: 'project-scope-path',
        command: 'kb config set plugins.commit.llm.enabled true',
      },
    ));

    expect(envelope).toMatchObject({
      code: 'KB_CONFIG_INVALID',
      message: 'Configuration path commit.llm.enabled is not valid at project scope; project plugin settings must use plugins.<configSection>.<key>.',
      hint: 'Run "kb config set plugins.commit.llm.enabled true" to write the project setting.',
      details: { path: 'commit.llm.enabled' },
    });
  });
});
