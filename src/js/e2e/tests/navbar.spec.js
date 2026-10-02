// The header: its refresh status and button, the page data the app preloads in
// the background, and the tabs for optional server features.
const {
  test: base,
  expect,
  pageDataLoaded,
  roleConfig,
  createConfig,
  deleteConfig,
} = require('./helpers');

const test = base.extend({
  // a Role config the test creates part way through: absent when it starts
  // and removed when it ends
  role: async ({ request }, use) => {
    const config = roleConfig('e2e-refresh-role');
    await deleteConfig(request, config);
    await use(config);
    await deleteConfig(request, config);
  },
});

const isConfigs = (r) => r.url().endsWith('/api/v1/configs');

// Holds the page's GET /api/v1/configs requests so a test can look at a page
// while its load is still running. The returned release() waits until at
// least one is held, then lets every held one go.
async function holdConfigLoads(page) {
  const held = [];
  let arrived = () => {};
  await page.route('**/api/v1/configs', (route) => {
    if (route.request().method() !== 'GET') return route.continue();
    held.push(route);
    arrived();
  });
  return async () => {
    while (!held.length) await new Promise((resolve) => (arrived = resolve));
    await Promise.all(
      held.splice(0).map((route) =>
        // a request the page canceled is reported by the test instead
        route.continue().catch((err) => {
          if (!route.request().failure()) throw err;
        }),
      ),
    );
  };
}

test('pages show preloaded data at once, and a load left behind still fills it', async ({
  page,
  request,
  role,
}) => {
  const nav = page.getByRole('navigation');
  const refreshing = page.getByText('Refreshing…');
  const refresh = page.getByRole('button', {
    name: "Refresh this page's data",
  });
  const roleRow = page.locator('tbody tr', { hasText: role.metadata.name });

  // once the app is idle it preloads every tab's data, Configs included
  const preloaded = page.waitForResponse(isConfigs);
  await page.goto('/experiments');
  await pageDataLoaded(page);
  await (await preloaded).finished();

  const release = await holdConfigLoads(page);

  // the preloaded rows show while the page's own load runs
  await nav.getByRole('link', { name: 'Configs' }).click();
  await expect(
    page.locator('tbody tr', { hasText: 'global-admin' }),
  ).toBeVisible();
  await expect(refreshing).toBeVisible();
  await expect(refresh).toBeDisabled();

  // leave while the load runs; it finishes in the background, rather than
  // being canceled, and fills the cache for the next visit
  await createConfig(request, role);
  const leftBehind = Promise.race([
    page.waitForResponse(isConfigs).then(async (response) => {
      await response.finished();
      return 'finished';
    }),
    page
      .waitForEvent('requestfailed', isConfigs)
      .then((req) => `failed: ${req.failure()?.errorText}`),
  ]);
  await nav.getByRole('link', { name: 'Users' }).click();
  await pageDataLoaded(page);
  await release();
  expect(await leftBehind, 'the Configs load left behind').toBe('finished');

  await nav.getByRole('link', { name: 'Configs' }).click();
  await expect(roleRow).toBeVisible();
  await expect(refreshing).toBeVisible();
  await release();
  await pageDataLoaded(page);

  // the button reloads the page's data
  await deleteConfig(request, role);
  await expect(roleRow).toBeVisible();
  await refresh.click();
  await expect(refreshing).toBeVisible();
  await release();
  await expect(roleRow).toBeHidden();
  await pageDataLoaded(page);
});

test('tabs for optional server features follow /features', async ({ page }) => {
  const nav = page.getByRole('navigation');
  // answers /features as a server without, then with, WebShark and the
  // tunneler downloads would
  const offer = (features) =>
    page.route('**/features', (route) => route.fulfill({ json: { features } }));

  await offer([]);
  await page.goto('/experiments');
  await pageDataLoaded(page);
  await expect(nav.getByRole('link', { name: 'WebShark' })).toHaveCount(0);
  await expect(nav.getByRole('link', { name: 'Tunneler' })).toHaveCount(0);
  await page.goto('/packets');
  await expect(
    page.getByText('WebShark is not installed on this phēnix server.'),
  ).toBeVisible();

  await page.unrouteAll();
  await offer(['webshark', 'tunneler-download']);
  await page.goto('/experiments');
  await expect(nav.getByRole('link', { name: 'Tunneler' })).toBeVisible();
  await nav.getByRole('link', { name: 'WebShark' }).click();
  await expect(page.getByLabel('Experiment')).toBeVisible();
});
