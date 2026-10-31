// First-visit navigation over an emulated high-latency link: any page chunk not
// prefetched makes the first click on its nav tab blow the time budget.
const { test, expect } = require('./helpers');

const LATENCY_MS = 1000;

// Nav tabs with auth disabled and an empty store; WebShark shows only when offered,
// and Tunneler is a header button.
const tabs = [
  '/configs/',
  '/disks/',
  '/hosts',
  '/users',
  '/log',
  '/scorch',
  '/soh',
  '/console',
  '/settings',
  '/experiments',
];

// Resolve once no new resource has loaded for `quietMs`, i.e. once the idle
// prefetch has finished.
async function waitForResourcesQuiet(page, quietMs = 1500, timeoutMs = 60000) {
  const deadline = Date.now() + timeoutMs;
  let last = -1;
  let quietSince = Date.now();
  while (Date.now() < deadline) {
    const count = await page.evaluate(
      () => performance.getEntriesByType('resource').length,
    );
    if (count !== last) {
      last = count;
      quietSince = Date.now();
    } else if (Date.now() - quietSince >= quietMs) {
      return;
    }
    await page.waitForTimeout(250);
  }
  throw new Error('resources never went quiet');
}

test.describe('first-visit navigation', () => {
  test.skip(
    ({ browserName }) => browserName !== 'chromium',
    'network emulation uses the Chrome DevTools Protocol',
  );

  test('nav tabs open without fetching code on click', async ({ page }) => {
    await page.goto('/experiments');
    await waitForResourcesQuiet(page);

    const cdp = await page.context().newCDPSession(page);
    await cdp.send('Network.enable');
    await cdp.send('Network.emulateNetworkConditions', {
      offline: false,
      latency: LATENCY_MS,
      downloadThroughput: (4 * 1024 * 1024) / 8,
      uploadThroughput: (4 * 1024 * 1024) / 8,
    });

    let assetRequests = [];
    page.on('request', (req) => {
      if (new URL(req.url()).pathname.includes('/assets/')) {
        assetRequests.push(req.url());
      }
    });

    const slow = [];
    for (const path of tabs) {
      assetRequests = [];
      const start = Date.now();
      await page.locator(`a.navbar-item[href$="${path}"]`).first().click();
      await page.waitForURL((url) => url.pathname === path);
      const elapsed = Date.now() - start;

      expect(
        assetRequests,
        `opening ${path} should not wait on fetching code`,
      ).toEqual([]);
      if (elapsed >= LATENCY_MS) {
        slow.push(`${path}: ${elapsed} ms`);
      }
    }

    expect(slow, `tabs slower than ${LATENCY_MS} ms`).toEqual([]);
  });
});
