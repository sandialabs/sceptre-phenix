// The header: refresh, preloaded page data, optional feature links, menus,
// the jobs menu, and one row of items from the burger menu's breakpoint up.
const {
  test: base,
  expect,
  expectAccessible,
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

// A background job as GET /api/v1/jobs lists it.
const job = (id, fields) => ({
  id,
  kind: 'vms.action',
  owner: 'global-admin',
  experiment: 'e2e',
  title: 'Restart 3 VMs',
  status: 'running',
  cancelable: true,
  progress: { done: 1, total: 3 },
  items: [],
  created: '2026-10-14T15:00:00Z',
  updated: '2026-10-14T15:00:01Z',
  revision: 2,
  ...fields,
});
const RUNNING = job('j0000000000000001', {});
const PARTIAL = job('j0000000000000002', {
  kind: 'files.archive',
  title: 'Archive 2 files of e2e',
  status: 'partial',
  progress: { done: 2, total: 2, bytes: 10, bytesTotal: 10 },
  artifact: { name: 'e2e-files-incomplete.zip', size: 10, incomplete: true },
  created: '2026-10-14T14:00:00Z',
});

// Answers the job list with the jobs in `jobs` at the time of each request.
const standInJobs = (page, jobs) =>
  page.route(
    (url) => url.pathname.endsWith('/api/v1/jobs'),
    (route) => route.fulfill({ json: { jobs } }),
  );

// Gives the header every optional item, the jobs in `jobs` and the longest
// refresh tooltip, with no experiments: other tests' experiments can widen it.
async function standInFullHeader(page, jobs) {
  await page.route('**/features', (route) =>
    route.fulfill({ json: { features: ['webshark', 'tunneler-download'] } }),
  );
  await standInJobs(page, jobs);
  await page.route(
    (url) => url.pathname.endsWith('/api/v1/experiments'),
    async (route) => {
      if (route.request().method() !== 'GET') return route.continue();
      const response = await route.fetch();
      const now = Date.now();
      const { 'content-length': _, ...headers } = response.headers();
      await route.fulfill({
        json: { experiments: [], total: 0 },
        headers: {
          ...headers,
          'x-phenix-stale': 'true',
          'x-phenix-observed-at': new Date(now - 150_000).toUTCString(),
          date: new Date(now).toUTCString(),
        },
      });
    },
  );
}

test('the app preloads page data once idle, and the refresh button reloads it from the server', async ({
  page,
  request,
  role,
}) => {
  const nav = page.getByRole('navigation');
  const roleRow = page.locator('tbody tr', { hasText: role.metadata.name });

  // once the app is idle it preloads every tab's data, Configs included
  const preloaded = page.waitForResponse(isConfigs);
  await page.goto('/experiments');
  await (await preloaded).finished();

  // the preloaded rows show while the page's own load runs
  const release = await holdConfigLoads(page);
  await nav.getByRole('button', { name: 'Resources' }).click();
  await nav.getByRole('link', { name: 'Configs' }).click();
  await expect(
    page.locator('tbody tr', { hasText: 'global-admin' }),
  ).toBeVisible();
  await release();
  await page.unroute('**/api/v1/configs');
  await pageDataLoaded(page);
  await createConfig(request, role);
  await expect(roleRow).toBeHidden();

  await page.getByRole('button', { name: "Refresh this page's data" }).click();
  await expect(roleRow).toBeVisible();
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
  await expect(page.getByLabel('Experiment', { exact: true })).toBeVisible();
});

test('the header jobs menu lists the jobs the server has', async ({ page }) => {
  await standInJobs(page, [RUNNING, PARTIAL]);
  await page.goto('/experiments');
  await page
    .getByRole('button', {
      name: 'Background jobs (1 job running; 1 partly succeeded job not viewed)',
    })
    .click();

  const panel = page.getByRole('region', { name: 'Background jobs' });
  await expect(panel).toContainText('Restart 3 VMs');
  await expect(panel).toContainText('Archive 2 files of e2e');
});

test(
  'the header menus, the refresh tooltip and the jobs menu pass the scan',
  { tag: '@axe' },
  async ({ page }) => {
    const nav = page.getByRole('navigation', { name: 'main navigation' });
    const refresh = nav.getByRole('button', {
      name: "Refresh this page's data",
    });
    const jobs = [];
    await standInFullHeader(page, jobs);
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto('/experiments');
    await expect(refresh).toHaveAccessibleName(/may be out of date/);

    await nav
      .getByRole('button', { name: 'Background jobs (0 jobs running)' })
      .click();
    await expect(page.getByText('No background jobs')).toBeVisible();
    await expectAccessible(page, 'jobs-menu-empty');
    await page.keyboard.press('Escape');
    await expect(page.getByText('No background jobs')).toBeHidden();

    const admin = nav.getByRole('button', { name: 'Admin', exact: true });
    const first = page.locator('#nav-admin-panel a').first();
    await admin.focus();
    await page.keyboard.press('Enter');
    await expect(first).toBeVisible();
    await expectAccessible(page, 'header-menu-open');
    await page.keyboard.press('Escape');
    await expect(first).toBeHidden();

    await refresh.hover();
    await expect(nav.locator('.refresh-status .tooltip-content')).toBeVisible();
    await expectAccessible(page, 'header-refresh-tooltip');

    jobs.push(RUNNING, PARTIAL);
    await page.reload();
    await nav
      .getByRole('button', { name: /^Background jobs \(1 job running;/ })
      .click();
    await expect(
      page.getByRole('region', { name: 'Background jobs' }),
    ).toContainText('Archive 2 files of e2e');
    await expectAccessible(page, 'jobs-menu');
  },
);

// The top-level links and the links in each menu that a role allowed
// everything gets on a server with WebShark.
const LINKS = ['Experiments', 'Builder', 'SCORCH', 'SoH', 'WebShark'];
const MENUS = {
  Resources: ['Configs', 'Disks', 'Hosts', 'VLANs'],
  Admin: ['Users', 'Logs', 'Settings', 'Console'],
};

// Resizes the window and waits two frames for the layout to settle.
async function resize(page, width) {
  await page.setViewportSize({ width, height: 800 });
  await page.evaluate(
    () =>
      new Promise((resolve) => {
        requestAnimationFrame(() => requestAnimationFrame(resolve));
      }),
  );
}

// The header's rows (items that start below every item before them start a
// new row) and the page's horizontal overflow.
const headerLayout = (page) =>
  page.evaluate(() => {
    const items = [
      ...document.querySelectorAll(
        '.navbar-brand > *, .navbar-start > *, .navbar-end > *',
      ),
    ].filter((item) => item.checkVisibility());
    const boxes = items
      .map((item) => item.getBoundingClientRect())
      .sort((a, b) => a.top - b.top);
    let rows = 0;
    let bottom = -Infinity;
    for (const box of boxes) {
      if (box.top >= bottom - 1) rows += 1;
      bottom = Math.max(bottom, box.bottom);
    }
    return {
      rows,
      right: Math.max(...boxes.map((box) => box.right)),
      overflow: document.documentElement.scrollWidth - window.innerWidth,
    };
  });

// the width of the first link's text, 1 or less where only its icon shows
const textWidth = async (nav) =>
  (await nav.locator('.nav-text').first().boundingBox())?.width ?? 0;

test('the header stays one row from 1024 px wide, with every item in reach', async ({
  page,
}) => {
  const nav = page.getByRole('navigation', { name: 'main navigation' });
  const refresh = nav.getByRole('button', {
    name: "Refresh this page's data",
  });

  await standInFullHeader(page, [RUNNING]);

  await page.goto('/experiments');
  await expect(refresh).toHaveAccessibleName(
    /min ago · may be out of date: minimega was busy/,
  );
  await expect(
    nav.getByRole('button', { name: 'Background jobs (1 job running)' }),
  ).toBeVisible();

  for (const width of [1024, 1280, 1366, 1440, 1600, 1920]) {
    await test.step(`${width} px wide`, async () => {
      await resize(page, width);
      const layout = await headerLayout(page);
      expect.soft(layout.rows, 'header rows').toBe(1);
      expect
        .soft(layout.right, 'right edge of the last item')
        .toBeLessThanOrEqual(width);
      expect
        .soft(layout.overflow, 'page scroll past the window')
        .toBeLessThanOrEqual(0);

      // only the icons show up to 1400 px; the names stay
      const shown = await textWidth(nav);
      if (width < 1400) expect.soft(shown, 'link text').toBeLessThanOrEqual(1);
      else expect.soft(shown, 'link text').toBeGreaterThan(1);
      for (const name of LINKS) {
        await expect
          .soft(nav.getByRole('link', { name, exact: true }))
          .toBeVisible();
      }

      for (const [name, held] of Object.entries(MENUS)) {
        const menu = nav.getByRole('button', { name, exact: true });
        await menu.click();
        await expect
          .soft(page.locator(`#nav-${name.toLowerCase()}-panel a`))
          .toHaveText(held);
        await page.keyboard.press('Escape');
        await expect.soft(menu).toBeFocused();
      }

      for (const name of ['Tunneler', 'Docs (opens in new tab)']) {
        await expect.soft(nav.getByRole('link', { name })).toBeVisible();
      }
      await expect.soft(refresh).toBeVisible();
      await expect
        .soft(nav.getByRole('button', { name: 'Logout', exact: true }))
        .toBeVisible();
    });
  }

  await test.step('the keyboard opens a menu, reaches its links and closes it', async () => {
    await resize(page, 1280);
    const menu = nav.getByRole('button', { name: 'Admin', exact: true });
    await menu.focus();
    await page.keyboard.press('Enter');
    const first = page.locator('#nav-admin-panel a').first();
    await expect(first).toBeVisible();
    await page.keyboard.press('Tab');
    await expect(first).toBeFocused();
    await page.keyboard.press('Escape');
    await expect(first).toBeHidden();
    await expect(menu).toBeFocused();
  });

  await test.step('a tooltip names each icon, and the refresh tooltip says how old the data is', async () => {
    await resize(page, 1280);
    await nav.getByRole('link', { name: 'SoH', exact: true }).hover();
    await expect(
      nav.locator('.tooltip-content', { hasText: 'State of Health' }),
    ).toBeVisible();

    const tip = nav.locator('.refresh-status .tooltip-content');
    await refresh.hover();
    await expect(tip).toBeVisible();
    await expect(tip).toContainText(/Updated \d+ min ago · may be out of date/);
  });

  await test.step('the burger menu shows the link text and opens the menus in place', async () => {
    await resize(page, 900);
    await nav.getByRole('button', { name: 'menu', exact: true }).click();
    expect.soft(await textWidth(nav), 'link text').toBeGreaterThan(1);
    await expect.soft(nav.locator('.nav-dot').first()).toBeHidden();
    const menu = nav.getByRole('button', { name: 'Admin', exact: true });
    await menu.click();
    const users = nav.getByRole('link', { name: 'Users', exact: true });
    await expect(users).toBeVisible();
    const [above, below] = await Promise.all([
      menu.boundingBox(),
      users.boundingBox(),
    ]);
    expect.soft(below.y, 'menu link below its button').toBeGreaterThan(above.y);
    expect.soft(below.x, 'menu link in the row').toBeLessThan(above.width);
  });
});
