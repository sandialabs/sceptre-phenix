const { defineConfig, devices } = require('@playwright/test');

// Target server: a running `phenix ui` (any deployment). Override with
// E2E_BASE_URL. Tests assume the UI was built with VITE_AUTH=disabled unless
// the env-gated auth specs are enabled (see README).
module.exports = defineConfig({
  testDir: './tests',
  timeout: 120000,
  workers: 1,
  retries: process.env.CI ? 1 : 0,
  reporter: [['list']],
  use: {
    baseURL: process.env.E2E_BASE_URL || 'http://127.0.0.1:3000',
    viewport: { width: 1600, height: 900 },
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
  },
  projects: [
    {
      name: 'google-chrome',
      use: {
        ...devices['Desktop Chrome'],
        channel: 'chrome',
        viewport: { width: 1600, height: 900 },
      },
    },
    {
      name: 'firefox',
      use: {
        ...devices['Desktop Firefox'],
        viewport: { width: 1600, height: 900 },
      },
    },
  ],
  outputDir: './test-results',
});
