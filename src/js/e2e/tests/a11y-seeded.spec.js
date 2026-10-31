// Accessibility scans of the views, modals and editors that only render with
// data. Each test seeds its own topology, scenario, stopped experiment and
// user through the REST API (no minimega needed), scans with the same axe
// policy as routes.spec.js, and deletes what it created.
const crypto = require('node:crypto');
const { test: base, expect } = require('@playwright/test');
const {
  attachCapture,
  settle,
  fatalOf,
  gotoSeeded,
  expectNoAxeViolations,
  animationsDone,
  motionOffenders,
} = require('./helpers');

// Based on the Topology and Scenario examples in docs/content/configuration.md.
function topologyConfig(name) {
  const node = (hostname, address) => ({
    type: 'VirtualMachine',
    general: { hostname, snapshot: true },
    hardware: { os_type: 'linux', drives: [{ image: 'ubuntu.qc2' }] },
    network: {
      interfaces: [
        {
          name: 'IF0',
          vlan: 'EXP-1',
          address,
          mask: 24,
          gateway: '192.168.10.254',
          proto: 'static',
          type: 'ethernet',
        },
      ],
    },
  });
  return {
    apiVersion: 'phenix.sandia.gov/v1',
    kind: 'Topology',
    metadata: { name },
    spec: {
      nodes: [node('host-00', '192.168.10.1'), node('host-01', '192.168.10.2')],
    },
  };
}

function scenarioConfig(name, topology) {
  return {
    apiVersion: 'phenix.sandia.gov/v2',
    kind: 'Scenario',
    metadata: { name, annotations: { topology } },
    spec: {
      apps: [
        {
          name: 'protonuke',
          hosts: [{ hostname: 'host-01', metadata: { args: '-http' } }],
        },
      ],
    },
  };
}

async function post(request, url, data) {
  const response = await request.post(url, { data });
  expect(response.ok(), `POST ${url}: ${await response.text()}`).toBeTruthy();
}

// Random names keep retries and parallel workers and shards apart. The UI
// limits experiment names to 15 characters.
const test = base.extend({
  seed: async ({ request }, use) => {
    const id = crypto.randomBytes(4).toString('hex');
    const seed = {
      topology: `a11y-topo-${id}`,
      scenario: `a11y-scn-${id}`,
      experiment: `a11y-${id}`,
      user: `a11y-user-${id}`,
    };
    try {
      await post(request, '/api/v1/configs', topologyConfig(seed.topology));
      await post(
        request,
        '/api/v1/configs',
        scenarioConfig(seed.scenario, seed.topology),
      );
      await post(request, '/api/v1/experiments', {
        name: seed.experiment,
        topology: seed.topology,
        scenario: seed.scenario,
      });
      await post(request, '/api/v1/users', {
        username: seed.user,
        password: 'Testpass1!',
        first_name: 'Ada',
        last_name: 'Lovelace',
        role_name: 'Global Viewer',
      });
      await use(seed);
    } finally {
      // Deleting a stopped experiment without minimega can answer an error
      // after it removed the config, so the responses are not checked.
      const paths = [
        `/api/v1/experiments/${seed.experiment}`,
        `/api/v1/configs/scenario/${seed.scenario}`,
        `/api/v1/configs/topology/${seed.topology}`,
        `/api/v1/users/${seed.user}`,
      ];
      for (const path of paths) {
        await request.delete(path).catch(() => {});
      }
    }
  },
});

// Opens `path`, waits for `ready`, and returns the console and page errors.
async function open(page, path, ready) {
  const issues = [];
  attachCapture(page, issues);
  await gotoSeeded(page, path);
  await settle(page, 1000);
  expect(new URL(page.url()).pathname, 'route should not redirect').toBe(path);
  if (ready) {
    await expect(ready(page)).toBeVisible({ timeout: 20000 });
  }
  return issues;
}

async function scan(page, issues, label) {
  await animationsDone(page);
  const fatal = fatalOf(issues);
  expect.soft(fatal, JSON.stringify(fatal, null, 2)).toHaveLength(0);
  await expectNoAxeViolations(page, label);
}

// Above ten users the table pages; show every row so the seeded one is there.
async function showAllUsers(page) {
  await expect(page.locator('table tbody tr').first()).toBeVisible();
  const paginate = page.getByRole('checkbox', { name: 'Paginate' });
  if ((await paginate.count()) > 0 && (await paginate.isChecked())) {
    await page.getByText('Paginate', { exact: true }).click();
  }
}

// Filters the configs table to one name and opens that config's viewer.
async function openConfigViewer(page, kind, name) {
  await page.getByPlaceholder('Find a Config').fill(name);
  await page.keyboard.press('Escape');
  await page.getByRole('button', { name: `View ${kind} ${name}` }).click();
  const viewer = page.getByRole('dialog', { name: `${kind}/${name}` });
  await expect(viewer).toBeVisible();
  return viewer;
}

async function openConfigEditor(page, seed) {
  const viewer = await openConfigViewer(page, 'Topology', seed.topology);
  await viewer.getByRole('button', { name: 'Edit Config' }).click();
  await expect(page.locator('.ace_editor')).toBeVisible({ timeout: 20000 });
  await expect(page.locator('.ace_content')).toContainText(seed.topology, {
    timeout: 20000,
  });
}

const pages = [
  { path: () => '/experiments', ready: (p) => p.locator('tbody tr').first() },
  {
    path: (s) => `/experiment/${s.experiment}`,
    ready: (p) => p.getByText('host-00', { exact: true }).first(),
  },
  { path: (s) => `/soh/${s.experiment}` },
  { path: (s) => `/scorch/${s.experiment}` },
  { path: () => '/configs/', ready: (p) => p.locator('tbody tr').first() },
  { path: () => '/users', ready: (p) => p.locator('tbody tr').first() },
  { path: () => '/settings' },
  {
    path: () => '/glossary',
    ready: (p) => p.locator('#vlan'),
  },
];

for (const { path, ready } of pages) {
  const label = path({ experiment: '<name>' });
  test(`seeded ${label}`, async ({ page, seed }) => {
    const issues = await open(page, path(seed), ready);
    await scan(page, issues, label);
  });
}

test('seeded config viewer', async ({ page, seed }) => {
  const issues = await open(page, '/configs/', (p) =>
    p.locator('tbody tr').first(),
  );
  await openConfigViewer(page, 'Topology', seed.topology);
  await scan(page, issues, 'config viewer');
});

test('seeded config editor', async ({ page, seed }) => {
  const issues = await open(page, '/configs/', (p) =>
    p.locator('tbody tr').first(),
  );
  await openConfigEditor(page, seed);
  await scan(page, issues, 'config editor');
});

test('experiment create modal, topology chosen', async ({ page, seed }) => {
  const issues = await open(page, '/experiments');
  await page.getByRole('button', { name: 'Create a new experiment' }).click();
  const modal = page.locator('.modal.is-active');
  await expect(
    modal.getByText('Create a New Experiment', { exact: true }),
  ).toBeVisible();
  await modal.locator('select').first().selectOption(seed.topology);
  await expect(modal.locator('option', { hasText: seed.scenario })).toHaveCount(
    1,
  );
  await scan(page, issues, 'experiment create modal');
});

test('user create modal', async ({ page }) => {
  const issues = await open(page, '/users');
  await page.getByRole('button', { name: 'Create a new user' }).click();
  await expect(
    page.getByText('Create a New User', { exact: true }),
  ).toBeVisible();
  await scan(page, issues, 'user create modal');
});

test('user edit modal', async ({ page, seed }) => {
  const issues = await open(page, '/users');
  await showAllUsers(page);
  await page.getByRole('button', { name: `Edit user ${seed.user}` }).click();
  await expect(
    page.getByText(`User ${seed.user}`, { exact: true }),
  ).toBeVisible();
  await scan(page, issues, 'user edit modal');
});

// WCAG 2.1.2: the Ace editor must let the keyboard leave. With keyboard
// accessibility on, Enter starts typing and Escape stops, so Tab moves on.
test('config editor does not trap the keyboard', async ({ page, seed }) => {
  await open(page, '/configs/', (p) => p.locator('tbody tr').first());
  await openConfigEditor(page, seed);
  const editor = page.locator('.ace_editor');
  const scroller = editor.locator('.ace_scroller');
  const outside = () =>
    page.evaluate(() => !document.activeElement.closest('.ace_editor'));

  await scroller.focus();
  await page.keyboard.press('Enter');
  await expect(editor.locator('textarea.ace_text-input')).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(scroller).toBeFocused();
  await page.keyboard.press('Tab');
  expect(await outside(), 'Tab moves focus out of the editor').toBe(true);

  // Backward: Shift+Tab passes the gutter, the editor's other tab stop.
  await scroller.focus();
  await page.keyboard.press('Shift+Tab');
  await page.keyboard.press('Shift+Tab');
  expect(await outside(), 'Shift+Tab moves focus out of the editor').toBe(true);
});

// WCAG 2.3.3: with prefers-reduced-motion: reduce, nothing animates or
// transitions for longer than 0.01s.
test.describe('reduced motion', () => {
  test.use({ reducedMotion: 'reduce' });

  const views = [
    { label: '/experiments', path: () => '/experiments' },
    {
      label: '/experiments, create modal',
      path: () => '/experiments',
      act: (p) =>
        p.getByRole('button', { name: 'Create a new experiment' }).click(),
    },
    { label: '/experiment/<name>', path: (s) => `/experiment/${s.experiment}` },
    { label: '/configs/', path: () => '/configs/' },
    { label: '/users', path: () => '/users' },
  ];

  for (const { label, path, act } of views) {
    test(label, async ({ page, seed }) => {
      await open(page, path(seed));
      if (act) {
        await act(page);
        await expect(page.locator('.modal.is-active')).toBeVisible();
      }
      const offenders = await motionOffenders(page);
      expect(offenders, 'motion longer than 0.01s').toEqual([]);
    });
  }
});
