// Lighthouse CI: audits the built UI served by a running `phenix ui`.
// Override the target with E2E_BASE_URL (same variable as the e2e suite).
const base = (process.env.E2E_BASE_URL || 'http://127.0.0.1:3000').replace(
  /\/$/,
  '',
);

const routes = [
  '/experiments',
  '/hosts',
  '/configs/',
  '/disks/',
  '/users',
  '/log',
  '/settings',
  '/scorch',
];

module.exports = {
  ci: {
    collect: {
      url: routes.map((r) => base + r),
      numberOfRuns: 3,
      settings: {
        preset: 'desktop',
        // the UI talks to the backend over a websocket that never goes idle
        skipAudits: ['bf-cache'],
        chromeFlags: '--headless=new --no-sandbox',
      },
    },
    assert: {
      // medians across runs; see README.md for how to adjust budgets
      aggregationMethod: 'median-run',
      assertions: {
        'categories:performance': ['error', { minScore: 0.9 }],
        'first-contentful-paint': ['error', { maxNumericValue: 1500 }],
        'largest-contentful-paint': ['error', { maxNumericValue: 2000 }],
        'total-blocking-time': ['error', { maxNumericValue: 200 }],
        'cumulative-layout-shift': ['error', { maxNumericValue: 0.1 }],
        // static assets and API responses must be compressed, and static
        // assets long-cached, by the server
        'uses-text-compression': 'error',
        'uses-long-cache-ttl': 'error',
        // byte budgets leave room for growth: the UI runs on desktops and
        // laptops over fast links
        'total-byte-weight': ['error', { maxNumericValue: 1500000 }],
        // transferred script bytes, including the route chunks the UI
        // prefetches once idle (src/utils/prefetch.js), so it is well above
        // the entry chunk alone
        'resource-summary:script:size': ['error', { maxNumericValue: 600000 }],
      },
    },
    upload: {
      target: 'filesystem',
      outputDir: './lighthouse-results',
    },
  },
};
