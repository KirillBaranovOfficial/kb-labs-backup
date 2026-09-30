import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: '/opt/kb-studio-playwright',
  testMatch: ['fresh-host.spec.ts'],
  reporter: 'list',
  use: { headless: true },
});
