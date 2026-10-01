import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: '/opt/kb-studio-playwright',
  testMatch: ['**/*.spec.ts'],
  reporter: [
    ['json', { outputFile: process.env.KB_STUDIO_PLAYWRIGHT_JSON }],
    ['line'],
  ],
  timeout: 120_000,
  // Several case specs run in one journey step (fresh host, first tier, schedules).
  globalTimeout: 600_000,
  expect: { timeout: 10_000 },
  use: {
    headless: true,
    actionTimeout: 10_000,
    navigationTimeout: 15_000,
  },
});
