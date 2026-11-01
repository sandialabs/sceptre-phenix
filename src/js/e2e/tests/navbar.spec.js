// The header: its refresh status and button, the page data the app preloads in
// the background, and the links for optional server features.
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

// Holds the page's GET /api/v1/configs requests; release() waits until at least
// one is held, then lets every held one go.
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
    await Promise.all(held.splice(0).map((route) => route.continue()));
  };
}

test('pages show preloaded data at once, and the refresh button reloads it', async ({
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
  await createConfig(request, role);

  // the preloaded rows show while the page's own load runs
  await nav.getByRole('link', { name: 'Configs' }).click();
  await expect(
    page.locator('tbody tr', { hasText: 'global-admin' }),
  ).toBeVisible();
  await expect(refreshing).toBeVisible();
  await expect(refresh).toBeDisabled();
  await release();
  await expect(roleRow).toBeVisible();
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

test('links for optional server features follow /features', async ({
  page,
}) => {
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
  // Tunneler is a button at the right of the header, current on its page
  const tunneler = nav.getByRole('link', { name: 'Tunneler' });
  await expect(tunneler).toBeVisible();
  await tunneler.click();
  await expect(
    page.getByRole('heading', { name: 'Tunneler Downloads' }),
  ).toBeVisible();
  await expect(tunneler).toHaveAttribute('aria-current', 'page');
  await nav.getByRole('link', { name: 'WebShark' }).click();
  await expect(page.getByLabel('Experiment')).toBeVisible();
});
