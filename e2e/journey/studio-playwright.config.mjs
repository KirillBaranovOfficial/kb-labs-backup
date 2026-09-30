import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: '/opt/kb-studio-playwright',
  testMatch: ['fresh-host.spec.ts'],
  reporter: 'line',
  timeout: 120_000,
  globalTimeout: 150_000,
  expect: { timeout: 10_000 },
  use: {
    headless: true,
    actionTimeout: 10_000,
    navigationTimeout: 15_000,
  },
});
