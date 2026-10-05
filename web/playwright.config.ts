import { defineConfig, devices } from '@playwright/test'

// The end-to-end suite (web/e2e): a built server (bin/conductor, which embeds the generated workbench) with stub agents, driven
// through Chromium 1117, the revision @playwright/test 1.44.1 installs. make test-e2e builds both first. One worker: the specs
// share one server and its runs.
export default defineConfig({
  testDir: './e2e',
  timeout: 120_000,
  expect: { timeout: 15_000 },
  fullyParallel: false,
  workers: 1,
  retries: 0,
  forbidOnly: !!process.env.CI,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never', outputFolder: 'playwright-report' }]] : 'list',
  globalSetup: './e2e/global-setup.ts',
  globalTeardown: './e2e/global-teardown.ts',
  outputDir: 'test-results',
  use: {
    ...devices['Desktop Chrome'],
    viewport: { width: 1440, height: 900 },
    launchOptions: { args: ['--no-sandbox'] },
    trace: 'retain-on-failure',
  },
})
