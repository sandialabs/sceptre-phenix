const { defineConfig } = require('@playwright/test');

// Recaptures the docs/content/images screenshots (see docs-screenshots/README.md).
// Run on demand, never in CI: it overwrites the committed images.
module.exports = defineConfig({
  testDir: './docs-screenshots',
  timeout: 60000,
  workers: 1,
  retries: 0,
  reporter: [['list']],
  use: {
    baseURL: process.env.E2E_BASE_URL || 'http://127.0.0.1:3197',
    viewport: { width: 1600, height: 900 },
    deviceScaleFactor: 2,
    trace: 'retain-on-failure',
  },
  outputDir: './test-results/docs-screenshots',
});
