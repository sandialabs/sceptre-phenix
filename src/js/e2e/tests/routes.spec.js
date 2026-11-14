// Render every route with auth disabled and fail on any JS error. The @axe
// tests also fail on any WCAG 2.x A, AA or AAA violation reported by axe-core.
// Works against an empty store; no experiment or configs required.
// a11y-seeded.spec.js scans the views that need data.
const {
  test,
  expect,
  expectNoAxeViolations,
  diskImage,
  standInDisks,
} = require('./helpers');

// The header's refresh button once a page's data has loaded (it fails on Hosts
// without minimega), as its name says.
const dataLoaded = (page) =>
  page.getByRole('button', {
    name: /^Refresh this page's data, (updated just now|refresh failed)/,
  });

// Each route, the path it lands on when that differs, and what shows once it
// has loaded.
const routes = [
  // '/' redirects home
  { path: '/', lands: '/experiments', ready: dataLoaded },
  {
    path: '/signin',
    ready: (page) => page.getByRole('button', { name: 'Submit' }),
  },
  { path: '/experiments', ready: dataLoaded },
  { path: '/users', ready: dataLoaded },
  { path: '/settings', ready: dataLoaded },
  { path: '/configs/', ready: dataLoaded },
  { path: '/hosts', ready: dataLoaded },
  { path: '/vlans', ready: dataLoaded },
  { path: '/scorch', ready: dataLoaded },
  { path: '/soh', ready: dataLoaded },
  { path: '/vmtiles', ready: dataLoaded },
  { path: '/disks/', ready: dataLoaded },
  { path: '/log', ready: dataLoaded },
  {
    path: '/console',
    // a message, or the terminal when the server runs --minimega-console
    ready: (page) =>
      page
        .getByRole('heading', {
          name: /not configured|do not have access|Could not start/,
        })
        .or(page.getByLabel('Terminal input')),
  },
  {
    path: '/tunneler',
    ready: (page) =>
      page
        .getByText(
          /downloads are not installed|No tunneler builds|Could not list/,
        )
        .or(page.getByRole('link', { name: /^Download / }).first()),
  },
  {
    path: '/packets',
    ready: (page) =>
      page
        .getByText('WebShark is not installed on this phēnix server.')
        .or(page.getByLabel('Experiment', { exact: true })),
  },
  // '/disabled' bounces users whose role is not Disabled back to home
  { path: '/disabled', lands: '/experiments', ready: dataLoaded },
];

// a11y-seeded.spec.js scans these pages with data, so they get no scan here.
const seeded = new Set(['/experiments', '/users', '/settings', '/configs/']);

for (const r of routes) {
  test('route ' + r.path, async ({ page }) => {
    await page.goto(r.path);
    await expect(r.ready(page)).toBeVisible();
    expect(new URL(page.url()).pathname, 'route should land here').toBe(
      r.lands ?? r.path,
    );
  });
}

for (const r of routes.filter((r) => !seeded.has(r.lands ?? r.path))) {
  test('scan route ' + r.path, { tag: '@axe' }, async ({ page }) => {
    await page.goto(r.path);
    await expect(r.ready(page)).toBeVisible();
    await expectNoAxeViolations(page, r.path);
  });
}

// Opens the Disks page with images in folders and outside the files directory,
// which the empty store never lists, from a stood-in disk list.
async function openDisksWithImages(page) {
  // three images named e2e.qc2, the one in a folder backed by the one outside
  await standInDisks(page, [
    diskImage('/phenix/images/win/e2e.qc2', 'win/e2e.qc2', {
      backingImages: ['/data/vms/e2e.qc2'],
    }),
    diskImage('/phenix/images/e2e.qc2', 'e2e.qc2'),
    diskImage('/data/vms/e2e.qc2', ''),
  ]);
  await page.goto('/disks/');
  await expect(dataLoaded(page)).toBeVisible();
}

// disksPage.test.js checks the names, warnings and backing chain in detail;
// this test checks that real mouse clicks reach the warnings.
test('route /disks/ with images in folders and outside the files directory', async ({
  page,
}) => {
  await openDisksWithImages(page);
  const labels = page.locator('tbody .disk-label');
  await expect(labels).toHaveText([
    '/data/vms/e2e.qc2',
    'e2e.qc2',
    'win/e2e.qc2',
  ]);

  // a click explains the warning and does not open the disk's details as a
  // click elsewhere on its row does
  const outside = page.getByRole('button', {
    name: 'Outside the standard images directory',
  });
  const explanation = page.locator('.disk-warning-content');
  await outside.click();
  await expect(explanation).toBeVisible();
  const details = page.locator('.modal.is-active');
  await expect(details).toHaveCount(0);
  await page.keyboard.press('Escape');
  await expect(explanation).toBeHidden();

  await labels.getByText('win/e2e.qc2', { exact: true }).click();
  const title = details.locator('.modal-card-title');
  await expect(title).toHaveText('win/e2e.qc2');
  await details.getByRole('button', { name: '/data/vms/e2e.qc2' }).click();
  await expect(title).toHaveText('e2e.qc2');

  // Escape hides the Location row's explanation but leaves the window open (it
  // still takes a click); with no explanation showing, Escape closes the window.
  const shown = page.locator('.disk-warning-content:visible');
  const where = details.getByRole('button', {
    name: 'Outside the standard images directory',
  });
  await where.focus();
  await expect(shown).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(shown).toBeHidden();
  await where.click({ timeout: 5000 });
  await expect(shown).toBeVisible();
  await expect(title).toHaveText('e2e.qc2');
  await page.keyboard.press('Escape');
  await expect(shown).toBeHidden();
  await page.keyboard.press('Escape');
  await expect(details).toHaveCount(0);
});

test(
  'scan /disks/ with images and a backing chain',
  { tag: '@axe' },
  async ({ page }) => {
    await openDisksWithImages(page);

    const outside = page.getByRole('button', {
      name: 'Outside the standard images directory',
    });
    const explanation = page.locator('.disk-warning-content');
    await outside.focus();
    await expect(explanation).toBeVisible();
    await expectNoAxeViolations(page, '/disks/ with images');

    await page.keyboard.press('Escape');
    await expect(explanation).toBeHidden();
    await page
      .locator('tbody .disk-label')
      .getByText('win/e2e.qc2', { exact: true })
      .click();
    const details = page.locator('.modal.is-active');
    await expect(details.locator('.modal-card-title')).toHaveText(
      'win/e2e.qc2',
    );
    // axe checks colors as drawn, so the window has to finish fading in
    await details.evaluate((modal) =>
      Promise.all(
        modal.getAnimations({ subtree: true }).map((a) => a.finished),
      ),
    );
    await expectNoAxeViolations(page, '/disks/ details with a backing chain');
  },
);
