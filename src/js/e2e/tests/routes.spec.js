// Render every route with auth disabled and fail on any JS error or any
// WCAG 2.x A, AA or AAA violation reported by axe-core.
// Works against an empty store; no experiment or configs required.
// a11y-seeded.spec.js scans the views that need data.
const { test, expect } = require('@playwright/test');
const {
  attachCapture,
  settle,
  fatalOf,
  gotoSeeded,
  expectNoAxeViolations,
} = require('./helpers');

const routes = [
  '/',
  '/signin',
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
  '/glossary',
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

    await expectNoAxeViolations(page, r);
  });
}
