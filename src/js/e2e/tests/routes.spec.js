// Render every route with auth disabled and fail on any JS error or any
// WCAG 2.2 AA violation reported by axe-core.
// Works against an empty store; no experiment or configs required.
const { test, expect, expectAccessible } = require('./helpers');

// Pages that load data say in the header when the load has finished (it fails
// on Hosts without minimega); the rest show what they found.
const dataLoaded = (page) =>
  page.getByText('Updated just now').or(page.getByText('Refresh failed'));

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
        .or(page.getByLabel('Experiment')),
  },
  // '/disabled' bounces users whose role is not Disabled back to home
  { path: '/disabled', lands: '/experiments', ready: dataLoaded },
];

for (const r of routes) {
  test('route ' + r.path, async ({ page }) => {
    await page.goto(r.path);
    await expect(r.ready(page)).toBeVisible();
    expect(new URL(page.url()).pathname, 'route should land here').toBe(
      r.lands ?? r.path,
    );

    await expectAccessible(page, r.path);
  });
}

// The Disks page as it shows images in folders and outside the minimega files
// directory, which the empty store never lists: the server's disk list is
// stood in for.
test('route /disks/ with images in folders and outside the files directory', async ({
  page,
}) => {
  const disk = (fullPath, relativePath, backingImages = []) => ({
    kind: 'VM',
    name: fullPath.split('/').pop(),
    fullPath,
    relativePath,
    outsideFilesDir: !relativePath,
    readOnly: !relativePath,
    size: '1.0 MiB',
    virtualSize: '10 GiB',
    experiments: [],
    backingImages,
    inUse: false,
  });
  // three images named e2e.qc2, the one in a folder backed by the one outside
  const disks = [
    disk('/phenix/images/win/e2e.qc2', 'win/e2e.qc2', ['/data/vms/e2e.qc2']),
    disk('/phenix/images/e2e.qc2', 'e2e.qc2'),
    disk('/data/vms/e2e.qc2', ''),
  ];
  await page.route(
    (url) => url.pathname.endsWith('/api/v1/disks'),
    (route) =>
      route.request().method() === 'GET'
        ? route.fulfill({ json: { disks } })
        : route.continue(),
  );

  await page.goto('/disks/');
  await expect(dataLoaded(page)).toBeVisible();

  // named by their path within the files directory, or their full path,
  // sorted by that name
  const labels = page.locator('tbody .disk-label');
  await expect(labels).toHaveText([
    '/data/vms/e2e.qc2',
    'e2e.qc2',
    'win/e2e.qc2',
  ]);
  const outside = page.getByRole('button', {
    name: 'Outside the standard images directory',
  });
  await expect(outside).toHaveCount(1);
  await expect(
    page.locator('tbody tr', { has: outside }).locator('.disk-label'),
  ).toHaveText('/data/vms/e2e.qc2');
  // the warning explains itself on keyboard focus
  const explanation = page.locator('.disk-warning-content');
  await outside.focus();
  await expect(explanation).toBeVisible();
  await expectAccessible(page, '/disks/ with images');

  // and on a click, which does not open the disk's details as a click
  // elsewhere on its row does; it sits on the page body, where the table
  // cannot clip it
  await page.keyboard.press('Escape');
  await expect(explanation).toBeHidden();
  await outside.click();
  await expect(explanation).toBeVisible();
  await expect(page.locator('table .disk-warning-content')).toHaveCount(0);
  const details = page.locator('.modal.is-active');
  await expect(details).toHaveCount(0);
  await page.keyboard.press('Escape');
  await expect(explanation).toBeHidden();

  // the details window links each backing image by its full path, not by a
  // same-named image
  await labels.getByText('win/e2e.qc2', { exact: true }).click();
  const title = details.locator('.modal-card-title');
  await expect(title).toHaveText('win/e2e.qc2');
  // axe checks colors as drawn, so the window has to finish fading in
  await details.evaluate((modal) =>
    Promise.all(modal.getAnimations({ subtree: true }).map((a) => a.finished)),
  );
  await expectAccessible(page, '/disks/ details with a backing chain');
  await details.getByRole('button', { name: '/data/vms/e2e.qc2' }).click();
  await expect(title).toHaveText('/data/vms/e2e.qc2');

  // Escape hides the Location row's explanation and leaves the window open:
  // it still takes a click, which a closing window would not. With no
  // explanation showing, Escape closes the window.
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
  await expect(title).toHaveText('/data/vms/e2e.qc2');
  await page.keyboard.press('Escape');
  await expect(shown).toBeHidden();
  await page.keyboard.press('Escape');
  await expect(details).toHaveCount(0);
});
