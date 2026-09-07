// Render every route with auth disabled and fail on any JS error.
// Works against an empty store; no experiment or configs required.
const { test, expect } = require('@playwright/test');
const AxeBuilder = require('@axe-core/playwright').default;
const { attachCapture, settle, fatalOf, gotoSeeded } = require('./helpers');

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
];

for (const theme of ['light', 'dark']) {
  for (const r of routes) {
    test(`route ${r} (${theme})`, async ({ page }) => {
      await page.addInitScript((selectedTheme) => {
        window.localStorage.setItem('phenix.theme', selectedTheme);
      }, theme);

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

      const accessibility = await new AxeBuilder({ page })
        .exclude('.vue-devtools__anchor-btn')
        .withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'])
        .analyze();
      if (accessibility.violations.length > 0) {
        throw new Error(
          JSON.stringify(
            accessibility.violations.map((violation) => ({
              id: violation.id,
              nodes: violation.nodes.map((node) => ({
                target: node.target,
                html: node.html,
                message: node.any[0]?.message || node.failureSummary,
              })),
            })),
            null,
            2,
          ),
        );
      }
    });
  }
}
