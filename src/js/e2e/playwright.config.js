const { defineConfig, devices } = require('@playwright/test');

// Target server: a running `phenix ui` (any deployment). Override with
// E2E_BASE_URL. Tests assume the UI was built with VITE_AUTH=disabled unless
// the env-gated auth specs are enabled (see README).
//
// Projects:
// - chromium: every spec, except known-defect tests.
// - firefox: only tests tagged @cross-browser, a core Builder subset.
// - known-defects: tests tagged @known-defect (they assert the intended
//   behavior of an open defect and are expected to fail). They run once, in
//   Chromium, with a short assertion timeout so an expected failure is fast.
const viewport = { width: 1600, height: 900 };
const CI = Boolean(process.env.CI);

module.exports = defineConfig({
  testDir: './tests',
  timeout: 60000,
  fullyParallel: true,
  workers: process.env.E2E_WORKERS
    ? Number(process.env.E2E_WORKERS)
    : CI
      ? 4
      : '50%',
  retries: CI ? 1 : 0,
  reporter: [['list']],
  use: {
    baseURL: process.env.E2E_BASE_URL || 'http://127.0.0.1:3000',
    viewport,
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'], viewport },
      grepInvert: /@known-defect/,
    },
    {
      name: 'firefox',
      use: { ...devices['Desktop Firefox'], viewport },
      grep: /@cross-browser/,
      grepInvert: /@known-defect/,
    },
    {
      name: 'known-defects',
      use: { ...devices['Desktop Chrome'], viewport },
      grep: /@known-defect/,
      expect: { timeout: 3000 },
    },
  ],
  outputDir: './test-results',
});
