const { defineConfig, devices } = require('@playwright/test');

// Target server: a running `phenix ui` (any deployment). Override with
// E2E_BASE_URL. Tests assume the UI was built with VITE_AUTH=disabled unless
// the env-gated auth specs are enabled (see README).
const viewport = { width: 1600, height: 900 };
const CI = Boolean(process.env.CI);

// E2E_WORKERS takes what Playwright's workers takes: a count or a percentage.
function workerCount(value) {
  if (/^\d+%$/.test(value)) {
    return value;
  }
  const count = Number(value);
  if (!Number.isInteger(count) || count < 1) {
    throw new Error(
      `E2E_WORKERS must be a whole number or a percentage such as 50%, not "${value}"`,
    );
  }
  return count;
}

module.exports = defineConfig({
  testDir: './tests',
  timeout: 60000,
  // On CI the run stops after 8 minutes, about two times the slowest run.
  globalTimeout: CI ? 8 * 60 * 1000 : 0,
  fullyParallel: true,
  forbidOnly: CI,
  workers: process.env.E2E_WORKERS
    ? workerCount(process.env.E2E_WORKERS)
    : CI
      ? 4
      : '50%',
  // On CI a test that passes only on its retry fails the run. The retry
  // still runs, so the failed try keeps its trace.
  retries: CI ? 1 : 0,
  failOnFlakyTests: CI,
  reporter: [['list']],
  use: {
    baseURL: process.env.E2E_BASE_URL || 'http://127.0.0.1:3000',
    viewport,
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
  },
  // chromium runs all tests and firefox the @cross-browser ones.
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'], viewport },
    },
    {
      name: 'firefox',
      use: { ...devices['Desktop Firefox'], viewport },
      grep: /@cross-browser/,
    },
  ],
  outputDir: './test-results',
});
