import { vi } from 'vitest';

vi.mock('../../core/index.js', () => ({
  resolveAccount: vi.fn(() => ({ name: 'work' })),
  listAccounts: vi.fn(async () => []),
  listMessages: vi.fn(async () => []),
  listFolders: vi.fn(async () => []),
  getMessage: vi.fn(async () => ({})),
  getThread: vi.fn(async () => []),
  searchMessages: vi.fn(async () => []),
  sendMessage: vi.fn(async () => ({ messageId: 'sent' })),
  replyMessage: vi.fn(async () => ({ messageId: 'replied' })),
  deleteMessage: vi.fn(async () => undefined),
  moveMessage: vi.fn(async () => undefined),
  markMessage: vi.fn(async () => undefined),
}));

export const context = {
  ui: {
    info: vi.fn(),
    success: vi.fn(),
    chain: vi.fn(),
    sideBox: vi.fn(),
  },
};
