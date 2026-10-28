const { defineConfig, devices } = require('@playwright/test');

// Target server: a running `phenix ui` (any deployment). Override with
// E2E_BASE_URL. Tests assume the UI was built with VITE_AUTH=disabled unless
// the env-gated auth specs are enabled (see README).
const authMode = process.env.E2E_AUTH_MODE;
const viewport = { width: 1600, height: 900 };
const CI = Boolean(process.env.CI);
const chromium = { ...devices['Desktop Chrome'], viewport };
// Firefox runs only the @cross-browser tests, a browser-sensitive subset.
const firefox = { ...devices['Desktop Firefox'], viewport };
const crossBrowser = /@cross-browser/;

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

// Specs that never change the store run in parallel, before the rest run one
// at a time; a failure here skips the store specs.
const readOnly = [
  'routes.spec.js',
  'performance.spec.js',
  'jobs.spec.js',
  'health.spec.js',
];
const authSpecs = /auth-.*\.spec\.js/;

const projects = authMode
  ? // with auth on, only the mode's own spec and the health checks apply
    [
      {
        name: `auth-${authMode}`,
        use: chromium,
        testMatch: [`auth-${authMode}.spec.js`, 'health.spec.js'],
        workers: 1,
      },
      {
        name: `auth-${authMode}-firefox`,
        use: firefox,
        testMatch: [`auth-${authMode}.spec.js`, 'health.spec.js'],
        grep: crossBrowser,
        dependencies: [`auth-${authMode}`],
        workers: 1,
      },
    ]
  : [
      { name: 'read-only', use: chromium, testMatch: readOnly },
      {
        name: 'store',
        use: chromium,
        testIgnore: [...readOnly, authSpecs],
        dependencies: ['read-only'],
        workers: 1,
      },
      {
        name: 'firefox',
        use: firefox,
        testIgnore: authSpecs,
        grep: crossBrowser,
        dependencies: ['store'],
        workers: 1,
      },
    ];

module.exports = defineConfig({
  testDir: './tests',
  projects,
  timeout: 30000,
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
    baseURL: process.env.E2E_BASE_URL || 'http://127.0.0.1:3197',
    viewport,
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
  },
  outputDir: './test-results',
});
