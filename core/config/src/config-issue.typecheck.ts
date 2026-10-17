import type { ConfigIssue } from './user-config/write-user-config.js';

const validIssue: ConfigIssue = {
  path: 'adapters.llm',
  message: 'The adapter is unavailable.',
  code: 'KB_ADAPTER_NOT_INSTALLED',
  details: { package: '@kb-labs/adapters-example', slot: 'llm' },
};

const invalidIssue: ConfigIssue = {
  path: 'adapters.llm',
  message: 'The adapter is unavailable.',
  code: 'KB_ADAPTER_NOT_INSTALLED',
  // @ts-expect-error Config issues must use details declared for their selected code.
  details: { package: '@kb-labs/adapters-example', slot: 'llm', reason: 'missing' },
};

void validIssue;
void invalidIssue;
