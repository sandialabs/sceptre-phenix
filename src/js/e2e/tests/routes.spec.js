// Render every route with auth disabled and fail on any JS error.
// Works against an empty store; no experiment or configs required.
const { test, expect } = require('@playwright/test');
const { attachCapture, settle, fatalOf, gotoSeeded } = require('./helpers');

const routes = [
  '/',
  '/experiments',
  '/users',
  '/settings',
  '/configs/',
  '/hosts',
  '/scorch',
  '/vmtiles',
  '/disks/',
  '/log',
  '/console',
  '/tunneler',
  '/disabled',
];

for (const r of routes) {
  test('route ' + r, async ({ page }) => {
    const issues = [];
    attachCapture(page, issues);
    await gotoSeeded(page, r);
    await settle(page, 3000);

    const path = new URL(page.url()).pathname;
    if (r === '/' || r === '/disabled') {
      // '/' redirects home; '/disabled' bounces users whose role is not
      // Disabled back to home
      expect(path).toBe('/experiments');
    } else {
      expect(path, 'route should not redirect away').toBe(r);
    }

    const fatal = fatalOf(issues);
    expect(fatal, JSON.stringify(fatal, null, 2)).toHaveLength(0);
  });
}
