// Render every route with auth disabled and fail on any JS error or any
// WCAG 2.2 AA violation reported by axe-core.
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

    const accessibility = await new AxeBuilder({ page })
      .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa'])
      .analyze();
    await test.info().attach(`axe-${r}`, {
      body: JSON.stringify(
        {
          violations: accessibility.violations,
          incomplete: accessibility.incomplete,
        },
        null,
        2,
      ),
      contentType: 'application/json',
    });
    const violations = accessibility.violations;
    expect(
      violations.map((v) => `${v.id} (${v.nodes.length})`),
      'axe violations; see the axe attachment for details',
    ).toEqual([]);
  });
}
