// Each test writes the docs image it is named after (`-g disks_table` retakes
// one) from the sample data in fixtures.json, so any `phenix ui` built with
// VITE_AUTH=disabled will do.
const path = require('node:path');
const { test, expect } = require('@playwright/test');
const { pageDataLoaded } = require('../tests/helpers');
const {
  COLORS,
  VMS,
  standInConsoles,
  standInExperiment,
} = require('../tests/vnc-support');
const fixtures = require('./fixtures.json');
const vlansCluster = require('../../test/helpers/vlansFixture.json');

const IMAGES =
  process.env.DOCS_IMAGES_DIR ??
  path.resolve(__dirname, '../../../../docs/content/images');

const image = (name) => path.join(IMAGES, `${name}.png`);

// DOCS_SCREENSHOTS_DEBUG=1 logs each API request and websocket message that
// no sample answers, to find what a new image still needs.
const DEBUG = process.env.DOCS_SCREENSHOTS_DEBUG === '1';
const unanswered = (what) => DEBUG && console.warn(`no sample for ${what}`);

// The sample answers by "METHOD /path"; a key may end in a status, as
// "POST /experiments/x/start 400", for an answer other than 200.
function answers(overrides) {
  return new Map(
    Object.entries({ ...fixtures, ...overrides }).map(([key, body]) => {
      const [method, apiPath, status = '200'] = key.split(' ');
      return [`${method} ${apiPath}`, { status: Number(status), body }];
    }),
  );
}

// Answers the API requests named in fixtures.json, as "METHOD /path" under
// /api/v1, with their sample bodies; overrides replace or add bodies.
async function standIn(page, overrides = {}) {
  const responses = answers(overrides);
  await page.route(
    (url) => url.pathname.includes('/api/v1/'),
    (route) => {
      const request = route.request();
      const url = new URL(request.url());
      const key = `${request.method()} ${url.pathname.replace(/^.*\/api\/v1/, '')}`;
      const answer = responses.get(key);
      if (answer) {
        return route.fulfill({ status: answer.status, json: answer.body });
      }
      unanswered(`${key}${url.search}`);
      return route.fallback();
    },
  );
}

// Opens a page with the sample data and waits until it has drawn it.
async function open(page, url, overrides) {
  await standIn(page, overrides);
  await page.goto(url);
  await pageDataLoaded(page);
}

async function shoot(locator, name) {
  await locator.screenshot({
    path: image(name),
    animations: 'disabled',
    caret: 'hide',
  });
}

// Captures the smallest rectangle holding every locator, with pad pixels around it.
async function shootRegion(page, locators, name, pad = 8) {
  const boxes = await Promise.all(locators.map((l) => l.boundingBox()));
  const left = Math.max(0, Math.min(...boxes.map((b) => b.x)) - pad);
  const top = Math.max(0, Math.min(...boxes.map((b) => b.y)) - pad);
  const right = Math.max(...boxes.map((b) => b.x + b.width)) + pad;
  const bottom = Math.max(...boxes.map((b) => b.y + b.height)) + pad;
  await page.screenshot({
    path: image(name),
    clip: { x: left, y: top, width: right - left, height: bottom - top },
    animations: 'disabled',
    caret: 'hide',
  });
}

// Captures the whole table columns and rows that the locators reach, so no
// cell shows in part, and any part of a locator outside the table.
async function shootCells(table, locators, name) {
  const boxes = await Promise.all(locators.map((l) => l.boundingBox()));
  const want = {
    left: Math.min(...boxes.map((b) => b.x)),
    top: Math.min(...boxes.map((b) => b.y)),
    right: Math.max(...boxes.map((b) => b.x + b.width)),
    bottom: Math.max(...boxes.map((b) => b.y + b.height)),
  };
  const clip = await table.evaluate((el, want) => {
    const spans = (selector, start, end) =>
      [...el.querySelectorAll(selector)]
        .map((cell) => cell.getBoundingClientRect())
        .filter((r) => r[end] > want[start] + 1 && r[start] < want[end] - 1);
    const cols = spans('thead th', 'left', 'right');
    const rows = spans('tr', 'top', 'bottom');
    const left = Math.min(want.left, ...cols.map((r) => r.left));
    const top = Math.min(want.top, ...rows.map((r) => r.top));
    const right = Math.max(want.right, ...cols.map((r) => r.right));
    const bottom = Math.max(want.bottom, ...rows.map((r) => r.bottom));
    return { x: left, y: top, width: right - left, height: bottom - top };
  }, want);
  await table.page().screenshot({
    path: image(name),
    clip,
    animations: 'disabled',
    caret: 'hide',
  });
}

// Waits until every animation that ends has ended, such as a dialog that
// zooms in, so that the boxes of elements stay where they are.
async function settled(page) {
  await page.evaluate(() =>
    Promise.all(
      document
        .getAnimations()
        .filter((a) => a.effect?.getComputedTiming().endTime !== Infinity)
        .map((a) => a.finished.catch(() => {})),
    ),
  );
}

// Draws a red ring around a control, as the docs mark the one to click.
async function ring(locator) {
  await settled(locator.page());
  await locator.evaluate((el) => {
    const r = el.getBoundingClientRect();
    const mark = document.createElement('div');
    Object.assign(mark.style, {
      position: 'fixed',
      left: `${r.left - 6}px`,
      top: `${r.top - 6}px`,
      width: `${r.width + 12}px`,
      height: `${r.height + 12}px`,
      border: '3px solid #e8412c',
      borderRadius: '50%',
      pointerEvents: 'none',
      zIndex: '10000',
    });
    document.body.append(mark);
  });
}

const firstTable = (page) => page.locator('.b-table').first();

test('hosts_table', async ({ page }) => {
  await open(page, '/hosts');
  await shoot(firstTable(page), 'hosts_table');
});

// Shoots the refresh button with its tooltip, once the tooltip reads text.
async function shootRefresh(page, text, name) {
  const tip = page.locator('.refresh-status .tooltip-content');
  const button = page.locator('.refresh-status button');
  await button.hover();
  await expect(tip).toHaveText(text);
  await shootRegion(page, [page.locator('.refresh-status'), tip], name);
}

// The refresh button with its tooltip, which says when the data was updated.
test('header_refresh', async ({ page }) => {
  // wide enough that the navbar shows every label
  await page.setViewportSize({ width: 1920, height: 900 });
  await open(page, '/hosts');
  await shootRefresh(page, 'Updated just now', 'header_refresh');
});

// The same, once the server says it read the hosts 3 minutes ago: past the
// Hosts page's 90 s limit, so the data may be out of date.
test('header_refresh_stale', async ({ page }) => {
  await page.setViewportSize({ width: 1920, height: 900 });
  await standIn(page);
  await page.route(
    (url) => url.pathname.endsWith('/api/v1/hosts'),
    (route) =>
      route.fulfill({
        json: fixtures['GET /hosts'],
        headers: {
          date: new Date().toUTCString(),
          'x-phenix-observed-at': new Date(Date.now() - 180_000).toISOString(),
        },
      }),
  );
  // pageDataLoaded waits for fresh data, so the tooltip's text is the wait
  await page.goto('/hosts');
  await shootRefresh(
    page,
    'Updated 3 min ago · may be out of date: older than 90 s',
    'header_refresh_stale',
  );
});

test('users_search', async ({ page }) => {
  await open(page, '/users');
  await shootRegion(
    page,
    [
      page.getByRole('textbox', { name: 'Find a user' }),
      page.getByRole('button', { name: 'Create a new user' }),
      firstTable(page),
    ],
    'users_search',
    0,
  );
});

test('disks_table', async ({ page }) => {
  await open(page, '/disks');
  await shoot(firstTable(page), 'disks_table');
});

test('empty_table', async ({ page }) => {
  await open(page, '/disks', { 'GET /disks': { disks: [] } });
  await expect(page.getByText('No disk images found')).toBeVisible();
  await shoot(firstTable(page), 'empty_table');
});

test('disks_upload', async ({ page }) => {
  await open(page, '/disks');
  await page.getByRole('button', { name: 'Upload a disk' }).click();
  const dialog = page
    .locator('.modal-card')
    .filter({ hasText: 'Upload a Disk' });
  await expect(dialog).toBeVisible();
  await shoot(dialog, 'disks_upload');
});

// The details card of PHENIX-kali.qc2: the stand-in answers GET
// /disks/details with that image's details, whatever the query names. The
// window is tall enough for the whole card, which otherwise scrolls.
test('disks_details', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 1600 });
  await open(page, '/disks');
  await page.locator('.disk-label', { hasText: /^PHENIX-kali\.qc2$/ }).click();
  const dialog = page.locator('.modal-card.disk-card');
  await expect(
    dialog.getByText('MBR partition table, disk ID 0x8f3c2a11'),
  ).toBeVisible();
  await shoot(dialog, 'disks_details');
});

test('create_exp', async ({ page }) => {
  await open(page, '/experiments');
  await ring(page.getByRole('button', { name: 'Create a new experiment' }));
  const head = await page.locator('.b-table thead').first().boundingBox();
  await page.screenshot({
    path: image('create_exp'),
    clip: {
      x: 0,
      y: 0,
      width: page.viewportSize().width,
      height: head.y + head.height,
    },
    animations: 'disabled',
  });
});

// Opens the create experiment card with the bennu topology chosen.
async function createCard(page) {
  await open(page, '/experiments');
  await page.getByRole('button', { name: 'Create a new experiment' }).click();
  const card = page.locator('.create-exp-card');
  const scenarios = page.waitForResponse((r) =>
    r.url().includes('/topologies/bennu/scenarios'),
  );
  await card
    .getByRole('combobox', { name: 'Experiment Topology' })
    .selectOption('bennu');
  await scenarios;
  return card;
}

test('create_exp_dia', async ({ page }) => {
  const card = await createCard(page);
  await shoot(card, 'create_exp_dia');
});

test('create_exp_options', async ({ page }) => {
  const card = await createCard(page);
  await card.getByRole('button', { name: 'Advanced Options' }).click();
  await expect(
    card.getByRole('group', { name: 'Experiment Annotations' }),
  ).toBeVisible();
  await shoot(card.locator('.create-exp-advanced'), 'create_exp_options');
});

test('create_exp_annotations', async ({ page }) => {
  const card = await createCard(page);
  await card.getByRole('button', { name: 'Advanced Options' }).click();

  const nodes = card.getByRole('group', { name: 'Annotations for Every VM' });
  await nodes.getByRole('button', { name: 'Add annotation' }).click();
  await page.getByRole('menuitem', { name: /phenix\/startup-via-cc/ }).click();
  await nodes
    .getByRole('combobox', { name: 'phenix/startup-via-cc value' })
    .selectOption({ label: 'true' });
  await nodes.getByRole('button', { name: 'Add annotation' }).click();
  await page
    .getByRole('menuitem', { name: /phenix\/startup-autotunnel/ })
    .click();
  await nodes
    .getByRole('textbox', { name: 'phenix/startup-autotunnel value' })
    .fill('8080:80');

  const exp = card.getByRole('group', { name: 'Experiment Annotations' });
  await exp.getByRole('button', { name: 'Add annotation' }).click();
  await page.getByRole('menuitem', { name: /phenix\.workflow\/tags/ }).click();
  await exp
    .getByRole('textbox', { name: 'phenix.workflow/tags value' })
    .fill('team=red');
  await page.evaluate(() => document.activeElement?.blur());

  await shootRegion(page, [nodes, exp], 'create_exp_annotations', 16);
});

// A minute after the newest State of Health run, so "ago" labels read as a fresh check.
const sohNow =
  Math.max(
    ...fixtures['GET /soh'].experiments
      .map((e) => e.last_run)
      .filter(Boolean)
      .map((t) => Date.parse(t)),
  ) + 60_000;

test('soh_overview', async ({ page }) => {
  await page.clock.setFixedTime(sohNow);
  await open(page, '/soh');
  await shootRegion(
    page,
    [page.getByPlaceholder('Find an Experiment'), firstTable(page)],
    'soh_overview',
    0,
  );
});

test('soh_overview_detail', async ({ page }) => {
  await page.clock.setFixedTime(sohNow);
  await open(page, '/soh');
  // rows sort worst first, so the first expandable row is bennu's
  await page
    .getByRole('button', { name: /^Problems of / })
    .first()
    .click();
  const detail = page.locator('tr.detail').first();
  await expect(detail).toContainText('problems in bennu');
  await shoot(detail, 'soh_overview_detail');
});

// The layout picker above bennu's State of Health graph, on the default
// layout once it has drawn.
test('soh_layout_picker', async ({ page }) => {
  await open(page, '/soh/bennu');
  const picker = page.locator('.soh-layout-select');
  await expect(picker.getByRole('radio', { name: /^VLAN/ })).toBeChecked();
  await expect(picker.locator('.soh-layout-hint')).toHaveText(
    'Hosts boxed by VLAN (cola.js, default)',
  );
  await page.evaluate(() => document.activeElement?.blur());
  await shootRegion(page, [picker], 'soh_layout_picker');
});

// A desktop for the VNC images: a top panel and a terminal window with lines
// of text, in a different place and color for each VM.
function desktop(vm, x, y) {
  const accent = COLORS[vm] ?? [120, 120, 120];
  const left = vm === 'db' ? 180 : 60;
  if (y < 22) {
    return x >= 8 && x < 64 && y >= 7 && y < 15
      ? [200, 200, 200]
      : [32, 32, 32];
  }
  if (x >= left && x < left + 400 && y >= 70 && y < 380) {
    if (y < 92) return accent;
    const row = Math.floor((y - 100) / 18);
    const inText = y >= 100 && (y - 100) % 18 < 8;
    const length = 40 + ((row * 97) % 300);
    return inText && x >= left + 16 && x < left + 16 + length
      ? [190, 220, 190]
      : [16, 16, 16];
  }
  return [
    24 + Math.floor(y / 30),
    48 + Math.floor(y / 20),
    64 + Math.floor(y / 16),
  ];
}

const vncExperiment = (page) =>
  standInExperiment(page, { experiment: 'demo', screen: desktop });

const drawn = (page, name) =>
  expect(
    page.getByRole('application', { name }).locator('canvas'),
  ).toHaveJSProperty('width', 640);

// The console page in two columns, with keyboard input in the first pane.
test('vnc_console_split', async ({ page }) => {
  await vncExperiment(page);
  await page.goto('/vnc?vm=demo/web&vm=demo/db');
  await drawn(page, 'Console of VM web in experiment demo');
  await drawn(page, 'Console of VM db in experiment demo');
  await page
    .getByRole('application', { name: 'Console of VM web in experiment demo' })
    .locator('canvas')
    .click();
  await expect(
    page.getByRole('region', { name: 'Pane 1: web' }).getByText('Keyboard'),
  ).toBeVisible();
  // the toast of the chord hint covers the panes
  await page
    .locator('.toast')
    .evaluateAll((toasts) => toasts.forEach((t) => t.remove()));
  await shoot(page.locator('.vnc-page'), 'vnc_console_split');
});

// The live tiles of the VNC tab: two running VMs, one paused and one shut down.
test('vnc_tiles', async ({ page }) => {
  await vncExperiment(page);
  await page.goto('/experiment/demo?tab=vnc');
  await expect(page.locator('.vnc-tile.is-live')).toHaveCount(3);
  await drawn(page, 'Live screen of VM web in experiment demo');
  const tiles = page.locator('.vnc-tile-item');
  // the phēnix picture of the page background shows between the tiles
  await page.addStyleTag({ content: 'html { background-image: none; }' });
  await shootRegion(
    page,
    [page.locator('.vnc-tiles-toolbar'), tiles.first(), tiles.last()],
    'vnc_tiles',
    0,
  );
});

// Opens the console page of web in the stand-in demo experiment, once its
// screen has drawn, without the toast of the chord hint.
async function consolePage(page) {
  await page.goto('/vnc?vm=demo/web');
  await drawn(page, 'Console of VM web in experiment demo');
  await page
    .locator('.toast')
    .evaluateAll((toasts) => toasts.forEach((t) => t.remove()));
}

// The top bar of the console page with its Actions menu open.
test('vnc_actions_menu', async ({ page }) => {
  await vncExperiment(page);
  await consolePage(page);
  await page.locator('.vnc-actions-trigger').click();
  const menu = page.getByRole('menu', { name: 'VM actions' });
  await expect(menu).toBeVisible();
  await settled(page);
  await shootRegion(
    page,
    [page.getByRole('group', { name: 'VM actions' }), menu],
    'vnc_actions_menu',
    16,
  );
});

// The Port forwards panel of the console top bar: a forward that the user
// made, which it can remove, and one that another user made. The miniccc
// agent of web is active, so Add port forward is not greyed out.
test('vnc_forwards_panel', async ({ page }) => {
  await vncExperiment(page);
  const web = VMS.find((vm) => vm.name === 'web');
  await page.route(
    (url) => url.pathname === '/api/v1/experiments/demo/vms/web',
    (route) => route.fulfill({ json: { ...web, ccActive: true } }),
  );
  await page.route(
    (url) => url.pathname.endsWith('/experiments/demo/vms/web/forwards'),
    (route) =>
      route.fulfill({
        json: {
          listeners: [
            {
              exp: 'demo',
              vm: 'web',
              srcPort: 18080,
              dstHost: '127.0.0.1',
              dstPort: 80,
              owner: 'global-admin',
            },
            {
              exp: 'demo',
              vm: 'web',
              srcPort: 13389,
              dstHost: '127.0.0.1',
              dstPort: 3389,
              owner: 'analyst@example.com',
            },
          ],
        },
      }),
  );
  await consolePage(page);
  await page.getByRole('button', { name: 'Port forwards' }).click();
  const panel = page.getByRole('dialog', { name: 'Port forwards' });
  await expect(panel.getByText('13389')).toBeVisible();
  await settled(page);
  await shootRegion(
    page,
    [page.getByRole('group', { name: 'VM actions' }), panel],
    'vnc_forwards_panel',
    16,
  );
});

// The Experiments table, with its totals strip and the Actions column.
test('experiments_table', async ({ page }) => {
  await open(page, '/experiments');
  await shootRegion(
    page,
    [page.locator('.exp-toolbar'), firstTable(page)],
    'experiments_table',
    0,
  );
});

// Two experiments checked, so the Start, Stop and Delete buttons show.
test('experiments_selected', async ({ page }) => {
  await open(page, '/experiments');
  for (const name of ['bennu', 'grid-training']) {
    await page
      .locator('label.b-checkbox', { hasText: `Select experiment ${name}` })
      .click();
  }
  await expect(page.locator('.exp-selection-actions')).toBeVisible();
  await page.evaluate(() => document.activeElement?.blur());
  await shootRegion(
    page,
    [page.locator('.exp-toolbar'), firstTable(page)],
    'experiments_selected',
    0,
  );
});

// A minute after the newest sample log entry, so the default time window
// holds every entry.
const logsNow =
  Math.max(...fixtures['GET /logs'].map((entry) => entry.time)) / 1000 + 60_000;

test('logs_page', async ({ page }) => {
  await page.clock.setFixedTime(logsNow);
  await open(page, '/log');
  const lastRow = page.getByRole('button', {
    name: /run started exp=ics-water-plant/,
  });
  await expect(lastRow).toBeVisible();
  await shootRegion(
    page,
    [page.locator('.logs-toolbar'), lastRow],
    'logs_page',
    0,
  );
});

// The Settings page with the server's default settings.
test('settings_page', async ({ page }) => {
  await open(page, '/settings');
  await expect(page.getByText('Password Settings')).toBeVisible();
  await shoot(page.locator('.form-section'), 'settings_page');
});

// The navbar, with every tab the Global Admin role sees.
test('navbar', async ({ page }) => {
  // wide enough that the navbar shows every label
  await page.setViewportSize({ width: 1920, height: 900 });
  await open(page, '/experiments');
  await shoot(page.locator('nav.navbar'), 'navbar');
});

// The background jobs of GET /jobs, newest first: two running, a file archive
// of bennu with its zip, and a shutdown that partly succeeded.
const job = (id, fields) => ({
  id,
  kind: 'vms.action',
  owner: 'admin@example.com',
  cancelable: true,
  updated: fields.created,
  revision: 3,
  ...fields,
});
const BENNU_JOBS = {
  jobs: [
    job('j4c1e9a07b3d2f851', {
      experiment: 'bennu',
      title: 'Restart 4 VMs',
      params: { action: 'restart' },
      status: 'running',
      progress: { done: 1, total: 4 },
      items: [
        { name: 'site-1-hmi', status: 'succeeded' },
        { name: 'elasticsearch', status: 'running' },
        { name: 'fep', status: 'pending' },
        { name: 'web', status: 'pending' },
      ],
      created: '2026-09-29T15:46:40Z',
      started: '2026-09-29T15:46:40Z',
    }),
    job('j9b27d4e1c08a6f35', {
      kind: 'disk.clone',
      title: 'Clone ubuntu-22.04-base.qc2 to ubuntu-22.04-hmi.qc2',
      params: {
        disk: '/phenix/images/ubuntu-22.04-base.qc2',
        new: '/phenix/images/ubuntu-22.04-hmi.qc2',
      },
      status: 'running',
      progress: {
        done: 0,
        total: 1,
        bytes: 1288490188,
        bytesTotal: 2040109465,
      },
      items: [{ name: 'ubuntu-22.04-hmi.qc2', status: 'running' }],
      created: '2026-09-29T15:45:12Z',
      started: '2026-09-29T15:45:12Z',
    }),
    job('j2f8a61c5d94e0b7a', {
      kind: 'files.archive',
      experiment: 'bennu',
      title: 'Archive 3 files of bennu',
      status: 'succeeded',
      cancelable: false,
      progress: { done: 3, total: 3, bytes: 53139455, bytesTotal: 53139455 },
      items: [
        'ids-sensor_1_20260929154512_7c1d.pcap',
        'soh-report.json',
        'historian_0_20260929153001_2b8e.pcap',
      ].map((name) => ({ name, status: 'succeeded' })),
      artifact: { name: 'bennu-files.zip', size: 48916582 },
      created: '2026-09-29T15:41:03Z',
      started: '2026-09-29T15:41:03Z',
      finished: '2026-09-29T15:41:09Z',
    }),
    job('j7d03b9e2a15c4f68', {
      experiment: 'bennu',
      title: 'Shut down 2 VMs',
      params: { action: 'shutdown' },
      status: 'partial',
      cancelable: false,
      progress: { done: 2, total: 2 },
      items: [
        { name: 'historian', status: 'succeeded' },
        {
          name: 'ot-sim-2',
          status: 'failed',
          error: 'vm ot-sim-2 is locked with status snapshotting',
          retryable: true,
        },
      ],
      created: '2026-09-29T15:32:18Z',
      started: '2026-09-29T15:32:18Z',
      finished: '2026-09-29T15:33:02Z',
    }),
  ],
  total: 4,
};

// The open jobs menu, with BENNU_JOBS; opening it marks the finished jobs
// viewed, so the button is no longer colored.
test('jobs_menu', async ({ page }) => {
  await open(page, '/experiments', { 'GET /jobs': BENNU_JOBS });
  await page
    .getByRole('button', { name: /^Background jobs \(2 jobs running/ })
    .click();
  const panel = page.locator('.jobs-menu .dropdown-menu');
  await expect(panel.getByText('Shut down 2 VMs')).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Background jobs (2 jobs running)' }),
  ).toBeVisible();
  // off the button, which has a lighter border under the pointer
  await page.mouse.move(800, 800);
  await settled(page);
  await shootRegion(page, [page.locator('.nav-tools'), panel], 'jobs_menu');
});

// A minimega console session in bennu's namespace, as `phenix mm --attach`
// prints it, with `vm info` listing the sample VMs that minimega runs.
function consoleSession() {
  const prompt = 'minimega:/tmp/minimega/minimega[bennu]$ ';
  const rows = [['host', 'name', 'state', 'ip']];
  const vms = fixtures['GET /experiments/bennu/vms'].vms
    .filter((vm) => !vm.external)
    .toSorted(
      (a, b) => a.host.localeCompare(b.host) || a.name.localeCompare(b.name),
    );
  for (const vm of vms) {
    rows.push([vm.host, vm.name, vm.state, `[${vm.ipv4.join(' ')}]`]);
  }
  const widths = rows[0].map((_, i) =>
    Math.max(...rows.map((row) => row[i].length)),
  );
  const table = rows.map((row) =>
    row
      .map((cell, i) => cell.padEnd(i < row.length - 1 ? widths[i] : 0))
      .join(' | '),
  );
  return [
    "CAUTION: calling 'quit' will cause the minimega daemon to exit",
    "use 'disconnect' or ^d to exit just the minimega command line",
    '',
    `${prompt}.columns host,name,state,ip vm info`,
    ...table,
    prompt,
  ].join('\r\n');
}

// The Console page with its toolbar. The stand-in starts console 4242 in
// bennu's namespace, which shows that the server has the console on, and its
// websocket sends the made-up session.
test('console_tab', async ({ page }) => {
  await page.setViewportSize({ width: 1600, height: 500 });
  await page.route(
    (url) => url.pathname.endsWith('/console/4242/size'),
    (route) => route.fulfill({ status: 200, body: '' }),
  );
  let socket;
  await page.routeWebSocket(/\/api\/v1\/console\/4242\/ws(\?|$)/, (ws) => {
    socket = ws;
  });
  // the page has no data to refresh, so it shows no refresh status
  await standIn(page);
  await page.goto('/console');
  const consolePage = page.locator('.console-page');
  // the page attaches the terminal to the socket once it reads Connected
  await expect(consolePage.locator('.console-state')).toHaveText('Connected');
  socket.send(Buffer.from(consoleSession()));
  await expect(consolePage.locator('.xterm-rows')).toContainText('site-1-hmi');
  await expect(consolePage.locator('#console-namespace')).toHaveValue('bennu');
  await page.evaluate(() => document.activeElement?.blur());
  // the terminal shrinks to the room above the footer after it first draws
  await expect
    .poll(() =>
      page.evaluate(() => {
        const box = (s) => document.querySelector(s).getBoundingClientRect();
        return (
          box('footer').bottom <= window.innerHeight &&
          box('.xterm-screen').bottom <= box('.console-host').bottom
        );
      }),
    )
    .toBe(true);
  await shootRegion(
    page,
    [
      consolePage.locator('.console-toolbar'),
      consolePage.locator('.xterm-screen'),
    ],
    'console_tab',
    0,
  );
});

// The Create a New User dialog.
test('create_a_new_user', async ({ page }) => {
  await open(page, '/users');
  await page.getByRole('button', { name: 'Create a new user' }).click();
  const dialog = page
    .locator('.modal-card')
    .filter({ hasText: 'Create a New User' });
  await expect(dialog).toBeVisible();
  await shoot(dialog, 'create_a_new_user');
});

test('upload', async ({ page }) => {
  await open(page, '/configs');
  await page.getByRole('button', { name: 'Upload a new config' }).click();
  const dialog = page
    .locator('.modal-card')
    .filter({ hasText: 'Upload a Config' });
  await expect(dialog).toBeVisible();
  await shoot(dialog, 'upload');
});

// The config viewer, on the vm-viewer Role.
test('view', async ({ page }) => {
  await open(page, '/configs');
  await page.getByRole('button', { name: 'View Role vm-viewer' }).click();
  const dialog = page.locator('.modal-card').filter({ hasText: 'vm-viewer' });
  await expect(dialog.getByLabel('Config, as YAML')).toHaveValue(/VM Viewer/);
  await shoot(dialog, 'view');
});

// Waits for the Ace editor to draw the config.
async function editorShown(page, text) {
  const editor = page.locator('section.hero').first();
  await expect(editor.locator('.ace_editor')).toBeVisible();
  await expect(editor.getByText(text).first()).toBeVisible();
  return editor;
}

// The editor on an existing Scenario.
test('view_edit', async ({ page }) => {
  await open(page, '/configs');
  await page.getByRole('button', { name: 'Edit Scenario mirror' }).click();
  await shoot(await editorShown(page, 'mirrorBridge'), 'view_edit');
});

// The editor on a new config, with its template.
test('new_edit', async ({ page }) => {
  await open(page, '/configs');
  await page.getByRole('button', { name: 'Create a new config' }).click();
  await shoot(await editorShown(page, 'apiVersion'), 'new_edit');
});

// The editor on an existing Experiment, whose name and kind cannot change.
test('disabled', async ({ page }) => {
  await open(page, '/configs');
  await page.getByRole('button', { name: 'Edit Experiment bennu' }).click();
  const editor = await editorShown(page, 'baseDir');
  await expect(page.locator('#config-editor-name')).toBeDisabled();
  await shoot(editor, 'disabled');
});

// The two sign-in images need a server whose UI was built with
// VITE_AUTH=enabled and that runs with --jwt-signing-key; the others skip
// them. Run them alone with -g "login_create|create_new_account" and
// E2E_BASE_URL pointing at that server.
async function signInPage(page) {
  await standIn(page);
  await page.goto('/signin');
  const form = page.locator('form').first();
  await expect(form.getByRole('button', { name: 'Submit' })).toBeVisible();
  // the username field takes focus on its own; its focus ring is not the point
  await page.evaluate(() => document.activeElement?.blur());
  return form;
}

test('login_create', async ({ page }) => {
  const form = await signInPage(page);
  await ring(page.getByRole('button', { name: 'Create Account' }));
  await shootRegion(page, [form], 'login_create', 16);
});

test('create_new_account', async ({ page }) => {
  await signInPage(page);
  await page.getByRole('button', { name: 'Create Account' }).click();
  const dialog = page
    .locator('.modal-card')
    .filter({ hasText: 'Create a New Account' });
  await expect(dialog).toBeVisible();
  await shoot(dialog, 'create_new_account');
});

// A made-up screen for a running VM, at the width minimega's screenshots have
// until the page asks for another.
function vmScreen(vm) {
  const levels = [62, 81, 34].map((level, i) => {
    const height = (level * 150) / 100;
    const x = 60 + i * 110;
    return (
      `<rect x="${x}" y="80" width="60" height="150" fill="#fff" stroke="#556"/>` +
      `<rect x="${x}" y="${230 - height}" width="60" height="${height}" fill="${['#38bdf8', '#22c55e', '#facc15'][i]}"/>`
    );
  });
  const svg =
    '<svg xmlns="http://www.w3.org/2000/svg" width="200" height="150" viewBox="0 0 400 300">' +
    '<rect width="400" height="300" fill="#16324f"/>' +
    '<rect x="20" y="16" width="360" height="250" rx="6" fill="#e5ebf3"/>' +
    '<rect x="20" y="16" width="360" height="34" rx="6" fill="#2563a8"/>' +
    `<text x="34" y="39" font-family="sans-serif" font-size="16" fill="#fff">${vm.name}</text>` +
    levels.join('') +
    '<rect y="276" width="400" height="24" fill="#0b1726"/></svg>';
  return `data:image/svg+xml,${encodeURIComponent(svg)}`;
}

// Answers the websocket's VM list requests with the sample VMs of
// "GET /experiments/<name>/vms", each running VM with a made-up screen. No
// other message gets an answer, and nothing reaches the server. shown(vms,
// filter) gives the VMs that the server's search for filter finds.
async function standInSocket(page, shown = (vms) => vms) {
  let seq = 0;
  await page.routeWebSocket(/\/api\/v1\/ws(\?|$)/, (ws) => {
    ws.onMessage((data) => {
      const message = JSON.parse(String(data));
      const { type, name, action } = message.resource ?? {};
      const list = fixtures[`GET /experiments/${name}/vms`];
      if (type !== 'experiment/vms' || action !== 'list' || !list) {
        unanswered(`websocket ${type} ${action} ${name ?? ''}`);
        return;
      }
      seq += 1;
      const {
        filter = '',
        sort_column: by,
        sort_asc: asc,
      } = message.request ?? {};
      // the server sorts the page by name unless the request names a column
      const vms = shown(list.vms, filter)
        .toSorted(
          (a, b) =>
            (by && by !== 'name' ? 0 : a.name.localeCompare(b.name)) *
            (asc === false ? -1 : 1),
        )
        .map((vm) => (vm.running ? { ...vm, screenshot: vmScreen(vm) } : vm));
      ws.send(
        JSON.stringify({
          resource: { type, name, action },
          result: { ...list, vms, total: vms.length },
          seq,
        }),
      );
    });
  });
}

const vmButton = (page, name) =>
  page.locator('.b-table').getByRole('button', { name, exact: true });

// Opens the running bennu experiment, its VMs listed over the stand-in
// websocket. The clock reads the sample VMs' uptime after the start time, and
// the window is tall enough for a VM's whole details card unless height says.
// shown is as for standInSocket, and a search q opens the page with it typed.
async function openRunning(page, height = 1600, shown = undefined, q = '') {
  await page.clock.setFixedTime(Date.parse('2026-09-29T15:47:06Z'));
  await page.setViewportSize({ width: 1600, height });
  await standInSocket(page, shown);
  if (q) {
    await open(page, `/experiment/bennu?q=${encodeURIComponent(q)}`);
    return;
  }
  await open(page, '/experiment/bennu');
  await expect(vmButton(page, 'site-1-hmi')).toBeVisible();
}

// The screen of site-1-hmi for its live console: an HMI window with the
// levels of three tanks, over a desktop with a task bar.
function hmiScreen(x, y) {
  if (y >= 456) return [11, 23, 38];
  if (x < 40 || x >= 600 || y < 32 || y >= 424) return [22, 50, 79];
  if (y < 64) return [37, 99, 168];
  const tank = Math.floor((x - 120) / 150);
  const left = 120 + tank * 150;
  if (tank < 0 || tank > 2 || x >= left + 100 || y < 120 || y >= 380) {
    return [229, 235, 243];
  }
  if (x < left + 2 || x >= left + 98 || y < 122 || y >= 378) {
    return [85, 85, 102];
  }
  const level = [62, 81, 34][tank];
  if (y < 380 - (260 * level) / 100) return [255, 255, 255];
  return [
    [56, 189, 248],
    [34, 197, 94],
    [250, 204, 21],
  ][tank];
}

const cardScreen = (vm, x, y) =>
  vm === 'site-1-hmi' ? hmiScreen(x, y) : desktop(vm, x, y);

// The Experiment User role of api/config/default/experiment-user.yml, for
// the bennu experiment, as the server gives it at sign-in.
const bennuNames = ['bennu', 'bennu/*'];
const EXPERIMENT_USER = {
  name: 'Experiment User',
  policies: [
    [
      ['experiments', 'experiments/*'],
      ['list', 'get'],
    ],
    [
      ['vms', 'vms/*'],
      ['list', 'get', 'patch'],
    ],
    [['vms/redeploy'], ['update']],
    [['vms/captures'], ['create', 'delete']],
    [['vms/snapshots'], ['list', 'create', 'update']],
    [['vms/screenshot'], ['create']],
    [['experiments/files'], ['create']],
    [['vlans'], ['list', 'get']],
  ]
    .map(([resources, verbs]) => ({
      resources,
      resourceNames: bennuNames,
      verbs,
    }))
    .concat({ resources: ['hosts'], resourceNames: ['*'], verbs: ['list'] }),
};

// Signs the page in as a user with role, as a sign-in leaves the session.
async function signedInAs(page, username, role) {
  await page.addInitScript(
    ([user, signedInRole]) => {
      sessionStorage.setItem('phenix.user', user);
      sessionStorage.setItem('phenix.token', 'authorized');
      sessionStorage.setItem('phenix.role', JSON.stringify(signedInRole));
      sessionStorage.setItem('phenix.auth', 'true');
    },
    [username, role],
  );
}

// Opens the details card of a VM of the running experiment, once its details,
// snapshots and port forwards have loaded. Its live console connects to a
// stand-in RFB server.
async function vmCard(page, name = 'site-1-hmi', height = undefined) {
  await standInConsoles(page, {
    experiment: 'bennu',
    vms: [name],
    screen: cardScreen,
  });
  await openRunning(page, height);
  await vmButton(page, name).click();
  const card = page.locator('.modal-card.vm-card');
  await expect(card.locator('.vm-annotations')).toBeVisible();
  return card;
}

// Clicks an action of the open VM card, by its name in vmActions.js, and
// returns the dialog it opens, found by its text; the card's own tooltips
// hold some of the same words.
async function vmAction(page, card, action, text) {
  await card.locator(`.vm-action-${action}`).click();
  const dialog = page
    .locator('.modal-card:not(.vm-card)')
    .filter({ hasText: text });
  await expect(dialog).toBeVisible();
  return dialog;
}

test.describe('running experiment', () => {
  // the file names that dialogs suggest hold the time in the browser's zone
  test.use({ timezoneId: 'UTC' });

  test('vm_backing_image', async ({ page }) => {
    const card = await vmCard(page);
    const dialog = await vmAction(
      page,
      card,
      'commit',
      'Create disk image of the site-1-hmi VM',
    );
    await shoot(dialog, 'vm_backing_image');
  });

  test('vm_memory_snapshot', async ({ page }) => {
    const card = await vmCard(page);
    const dialog = await vmAction(
      page,
      card,
      'memorySnapshot',
      'Create a memory snapshot for the site-1-hmi VM',
    );
    await shoot(dialog, 'vm_memory_snapshot');
  });

  test('vm_snapshot_confirm', async ({ page }) => {
    const card = await vmCard(page);
    const dialog = await vmAction(
      page,
      card,
      'snapshot',
      'Create a Snapshot of the site-1-hmi VM',
    );
    await shoot(dialog, 'vm_snapshot_confirm');
  });

  // The live console on the details card, with its banners and its bar; a
  // click on the screen opens the VM's console page.
  test('vm_console_link', async ({ page }) => {
    const card = await vmCard(page);
    await drawn(page, 'Console of VM site-1-hmi in experiment bennu');
    const preview = card.locator('.vnc-preview');
    await expect(
      preview.getByRole('button', { name: 'Take control' }),
    ).toBeVisible();
    await shoot(preview, 'vm_console_link');
  });

  // The whole details card of the running site-1-hmi. The card is 90% of
  // the window tall, so the window holds its sections and little more.
  test('vm_details_card', async ({ page }) => {
    const card = await vmCard(page, 'site-1-hmi', 1080);
    await drawn(page, 'Console of VM site-1-hmi in experiment bennu');
    await expect(card.getByText('site-1-hmi_2026-09-29_1430')).toBeVisible();
    await page.evaluate(() => document.activeElement?.blur());
    await settled(page);
    await shoot(card, 'vm_details_card');
  });

  // The card of the shut-down historian: the actions it cannot take now are
  // grayed out.
  test('vm_details_disabled', async ({ page }) => {
    const card = await vmCard(page, 'historian', 960);
    await expect(card.locator('.vm-no-screen')).toBeVisible();
    await page.evaluate(() => document.activeElement?.blur());
    await settled(page);
    await shoot(card, 'vm_details_disabled');
  });

  // Kill grayed out for an Experiment User, whose role cannot delete VMs,
  // with the tooltip that names the permission.
  test('vm_role_denied', async ({ page }) => {
    await signedInAs(page, 'analyst@example.com', EXPERIMENT_USER);
    const card = await vmCard(page);
    const power = card
      .locator('.vm-section')
      .filter({ has: page.locator('.vm-power-buttons') });
    await settled(page);
    await power.locator('.vm-action-kill').hover();
    const tip = tooltip(page, 'Your role does not permit this action');
    await expect(tip).toBeVisible();
    await shootRegion(page, [power, tip], 'vm_role_denied', 0);
  });

  // The mount browser, with the vm-mount and file-server features on: the
  // stand-in answers the mount, which has no body, and the features request.
  // Its file list is 70% of the window tall, so the window is the usual 900.
  test('vm_mount_browser', async ({ page }) => {
    await page.route(
      (url) => url.pathname.endsWith('/features'),
      (route) =>
        route.fulfill({ json: { features: ['vm-mount', 'file-server'] } }),
    );
    await page.route(
      (url) => url.pathname.endsWith('/vms/site-1-hmi/mount'),
      (route) => route.fulfill({ status: 200, body: '' }),
    );
    const card = await vmCard(page, 'site-1-hmi', 900);
    const dialog = await vmAction(page, card, 'mount', 'site-1-hmi Mount');
    await expect(dialog.getByText('hmi-config.xml')).toBeVisible();
    await expect(
      dialog.getByRole('combobox', { name: 'Copy experiment file' }),
    ).toBeEnabled();
    await shoot(dialog, 'vm_mount_browser');
  });

  // site-1-hmi's row, capturing on its first interface.
  test('vm_capture_taps', async ({ page }) => {
    await openRunning(page);
    const row = page.locator('.b-table tbody tr').filter({
      has: page.getByRole('button', { name: 'site-1-hmi', exact: true }),
    });
    await shoot(row, 'vm_capture_taps');
  });

  // The tabs and the toolbar of the running bennu, with the netflow button
  // right of the stop button.
  test('exp_netflow_button', async ({ page }) => {
    await openRunning(page, 900);
    await expect(
      page.getByRole('button', { name: 'Start netflow capture' }),
    ).toBeVisible();
    await page.evaluate(() => document.activeElement?.blur());
    await shootRegion(
      page,
      [page.locator('.exp-toolbar'), page.locator('.b-tabs > nav.tabs')],
      'exp_netflow_button',
      2,
    );
  });

  test('vm_kill_confirm', async ({ page }) => {
    const card = await vmCard(page);
    const dialog = await vmAction(page, card, 'kill', 'Kill the site-1-hmi VM');
    await shoot(dialog, 'vm_kill_confirm');
  });

  // The VLAN dialog for site-1-hmi's first interface, chosen from its
  // network menu, with CONTROL picked.
  test('vm_change_vlan', async ({ page }) => {
    // a scroll closes the network menu, so the window holds every row
    await openRunning(page, 2400);
    const row = page.locator('.b-table tbody tr').filter({
      has: page.getByRole('button', { name: 'site-1-hmi', exact: true }),
    });
    await row
      .getByRole('button', {
        name: 'Network menu for interface 0 of VM site-1-hmi',
      })
      .click();
    await page.getByRole('menuitem', { name: 'Change VLAN…' }).click();
    const dialog = page
      .locator('.modal-card')
      .filter({ hasText: 'Change the VLAN' });
    await expect(dialog).toBeVisible();
    await dialog
      .getByRole('combobox', { name: 'New VLAN for interface 0' })
      .selectOption({ label: 'control (102)' });
    await shoot(dialog, 'vm_change_vlan');
  });

  // The Power section of a VM's details card, with one button circled; the
  // section's own padding holds the ring.
  async function powerButton(page, vm, action, name) {
    const card = await vmCard(page, vm);
    const power = card
      .locator('.vm-section')
      .filter({ has: page.locator('.vm-power-buttons') });
    await ring(power.locator(`.vm-action-${action}`));
    await shoot(power, name);
  }

  test('vm_power_pause', async ({ page }) => {
    await powerButton(page, 'site-1-hmi', 'pause', 'vm_power_pause');
  });

  test('vm_power_restart', async ({ page }) => {
    await powerButton(page, 'site-1-hmi', 'restart', 'vm_power_restart');
  });

  test('vm_power_shutdown', async ({ page }) => {
    await powerButton(page, 'site-1-hmi', 'shutdown', 'vm_power_shutdown');
  });

  // ot-sim-2 is paused, so its Start button reads Resume.
  test('vm_power_resume', async ({ page }) => {
    await powerButton(page, 'ot-sim-2', 'start', 'vm_power_resume');
  });

  // The redeploy dialog, with more CPUs and memory picked.
  test('vm_redeploy', async ({ page }) => {
    const card = await vmCard(page);
    const dialog = await vmAction(
      page,
      card,
      'redeploy',
      'Modify current settings and redeploy',
    );
    const disk = dialog.getByRole('combobox', {
      name: 'Disk for VM site-1-hmi',
    });
    await expect(
      disk.locator('option', { hasText: 'ubuntu-22.04-dev.qc2' }),
    ).toHaveCount(1);
    await dialog
      .getByRole('combobox', { name: 'CPUs for VM site-1-hmi' })
      .selectOption('4');
    await dialog
      .getByRole('combobox', { name: 'Memory for VM site-1-hmi' })
      .selectOption({ label: '8 GiB' });
    await shoot(dialog, 'vm_redeploy');
  });

  // The bennu VMs that the server's search finds for each search the images
  // type, by the rules of util/mm/search.go.
  const found = {
    'historian or relay or router': ['historian', 'relay-1', 'router'],
    'shutdown or capturing': ['historian', 'site-1-hmi'],
    '10.1.2.0/29': ['power-provider', 'router'],
    capturing: ['power-provider', 'router', 'site-1-hmi'],
  };
  const search = (vms, filter) =>
    filter ? vms.filter((vm) => found[filter]?.includes(vm.name)) : vms;

  // The sample VMs once a capture runs on each interface in 10.1.2.0/29:
  // router's second and power-provider's first.
  const subnetCaptured = (vms, filter) =>
    search(vms, filter).map((vm) => {
      const iface = { router: 1, 'power-provider': 0 }[vm.name];
      if (iface === undefined) return vm;
      const filepath = `/phenix/experiments/bennu/files/${vm.name}_${iface}_20260929154012_4b7e.pcap`;
      return { ...vm, captures: [{ vm: vm.name, interface: iface, filepath }] };
    });

  // Opens the running experiment with a search, the stand-in socket answering
  // it as shown says, and waits for the rows it finds. The search's own
  // debounce waits on a clock that the fixed time stops, so the page opens
  // with the search in its URL instead.
  async function openSearched(page, shown, q, rows) {
    await openRunning(page, 1200, shown, q);
    await expect(
      page.locator('.b-table tbody tr:has(td[data-label="Node"])'),
    ).toHaveCount(rows.length);
    // an external node's name is text, not a button
    for (const name of rows) {
      await expect(
        page.locator('td[data-label="Node"]').getByText(name, { exact: true }),
      ).toBeVisible();
    }
  }

  // The toolbar, tabs and table of the running experiment's VMs.
  const vmTableRegion = (page) => [
    page.locator('.exp-toolbar'),
    page.locator('.b-table').first(),
  ];

  // An external node, the shut-down historian, and the router, whose
  // Snapshot button says why it is grayed out. The Screenshot column is
  // hidden, as the page lets a user do.
  test('vm_row_actions', async ({ page }) => {
    await openSearched(page, search, 'historian or relay or router', [
      'historian',
      'relay-1',
      'router',
    ]);
    await page.getByRole('button', { name: 'Select visible columns' }).click();
    await page.locator('.dropdown-item').getByText('Screenshots').click();
    await page.keyboard.press('Escape');
    await expect(page.locator('th', { hasText: 'Screenshot' })).toHaveCount(0);
    const router = page.locator('.b-table tbody tr').filter({
      has: page.getByRole('button', { name: 'router', exact: true }),
    });
    await router.locator('.vm-row-snapshot').hover();
    const tip = tooltip(page, 'snapshots enabled');
    await expect(tip).toBeVisible();
    await shootRegion(page, [firstTable(page), tip], 'vm_row_actions', 0);
  });

  // Two VMs checked, so the toolbar of actions on the selected VMs shows.
  test('vms_multi_select', async ({ page }) => {
    await openRunning(page, 1200);
    for (const name of ['elasticsearch', 'fep']) {
      await page
        .locator('.b-table label.b-checkbox', { hasText: `Select VM ${name}` })
        .click();
    }
    await page.evaluate(() => document.activeElement?.blur());
    const third = page.locator('.b-table tbody tr').nth(2);
    await shootRegion(
      page,
      [page.locator('.experiment-header'), third],
      'vms_multi_select',
      0,
    );
  });

  test('vms_multi_running_combined_terms', async ({ page }) => {
    await openSearched(page, search, 'shutdown or capturing', [
      'historian',
      'site-1-hmi',
    ]);
    await shootRegion(
      page,
      vmTableRegion(page),
      'vms_multi_running_combined_terms',
      0,
    );
  });

  // The VMs of a subnet, with the capture tooltip of an address.
  test('vms_multi_capture_start', async ({ page }) => {
    await openSearched(page, search, '10.1.2.0/29', [
      'power-provider',
      'router',
    ]);
    await page
      .locator('.b-table tbody tr')
      .filter({
        has: page.getByRole('button', { name: 'power-provider', exact: true }),
      })
      .locator('td[data-label="IP"]')
      .getByText('10.1.2.5', { exact: true })
      .hover();
    await expect(tooltip(page, 'start packet capture')).toBeVisible();
    await shootRegion(page, vmTableRegion(page), 'vms_multi_capture_start', 0);
  });

  // The same subnet once both of its VMs capture on it.
  test('vms_multi_capture_stop', async ({ page }) => {
    await openSearched(page, subnetCaptured, '10.1.2.0/29', [
      'power-provider',
      'router',
    ]);
    await shootRegion(page, vmTableRegion(page), 'vms_multi_capture_stop', 0);
  });

  // Every VM with a capture running, the subnet's two among them.
  test('vms_multi_captures_stop_all', async ({ page }) => {
    await openSearched(page, subnetCaptured, 'capturing', [
      'power-provider',
      'router',
      'site-1-hmi',
    ]);
    await shootRegion(
      page,
      vmTableRegion(page),
      'vms_multi_captures_stop_all',
      0,
    );
  });

  test('vm_reset_disk_confirm', async ({ page }) => {
    const card = await vmCard(page);
    const dialog = await vmAction(
      page,
      card,
      'resetDisk',
      'Reset the Disk of the site-1-hmi VM',
    );
    await shoot(dialog, 'vm_reset_disk_confirm');
  });
});

// The VM table of the stopped grid-training experiment, whose settings can
// change before it starts; at 1600 wide its Persistence menus cut their text.
test('vm_settings_stopped', async ({ page }) => {
  await page.setViewportSize({ width: 1920, height: 1080 });
  await open(page, '/experiment/grid-training');
  const table = page.locator('.b-table').filter({ hasText: 'attacker' });
  await expect(table).toBeVisible();
  await shoot(table, 'vm_settings_stopped');
});

// The Files tab of bennu: captures, SCORCH archives and other files, newest
// first, as the server sorts them by date.
const BENNU_FILES = {
  files: [
    [
      'ids-sensor_1_20260929154512_7c1d.pcap',
      40265318,
      ['Packet Capture'],
      '2026-09-29T15:46:58Z',
    ],
    ['soh-report.json', 186368, [], '2026-09-29T15:46:00Z', true],
    [
      'historian_0_20260929153001_2b8e.pcap',
      12687769,
      ['Packet Capture'],
      '2026-09-29T15:36:44Z',
    ],
    [
      'scorch-run-1_2026-09-29T15-00-00Z.tgz',
      18874368,
      ['Scorch Artifact'],
      '2026-09-29T15:15:31Z',
    ],
    [
      'scorch-run-1_2026-09-29T09-00-00Z.tgz',
      9437184,
      ['Scorch Artifact'],
      '2026-09-29T09:21:09Z',
    ],
    [
      'scorch-run-0_2026-09-29T08-30-05Z.tgz',
      3145728,
      ['Scorch Artifact'],
      '2026-09-29T08:44:50Z',
    ],
    ['uploads/hmi-patch.msi', 48234496, [], '2026-09-29T08:12:44Z'],
    [
      'scorch-run-0_2026-09-28T16-10-00Z.tgz',
      2097152,
      ['Scorch Artifact'],
      '2026-09-28T16:25:13Z',
    ],
  ].map(([path, size, categories, date, plainText = false]) => ({
    name: path.split('/').pop(),
    path,
    date,
    size,
    categories,
    plainText,
    isDir: false,
  })),
  total: 8,
};

// opens bennu's Files tab with BENNU_FILES and returns its table
async function filesTab(page) {
  await openRunning(page, 1000);
  await standIn(page, { 'GET /experiments/bennu/files': BENNU_FILES });
  await page.locator('.tabs a', { hasText: 'Files' }).click();
  const table = page.locator('.b-table').filter({ hasText: 'soh-report.json' });
  await expect(table.locator('tbody tr')).toHaveCount(8);
  return table;
}

test('exp_files_tab', async ({ page }) => {
  const table = await filesTab(page);
  await shoot(table, 'exp_files_tab');
});

// Three files checked, so the actions on them show above the table.
test('exp_files_selected', async ({ page }) => {
  const table = await filesTab(page);
  for (const name of [
    'ids-sensor_1_20260929154512_7c1d.pcap',
    'soh-report.json',
    'historian_0_20260929153001_2b8e.pcap',
  ]) {
    await table
      .locator('tbody tr', { hasText: name })
      .locator('label.b-checkbox')
      .click();
  }
  const bar = page.locator('.files-bar');
  await expect(bar).toContainText('(3)');
  await shootCells(
    table,
    [bar, table.locator('tbody tr').nth(4)],
    'exp_files_selected',
  );
});

// The Tunneler page, opened from the Tunneler button in the header, with each
// build the server offers.
test('tunneler_page', async ({ page }) => {
  await page.setViewportSize({ width: 1600, height: 1600 });
  await page.route(
    (url) => url.pathname.endsWith('/features'),
    (route) => route.fulfill({ json: { features: ['tunneler-download'] } }),
  );
  await page.route(
    (url) => url.pathname.endsWith('/downloads/tunneler'),
    (route) =>
      route.fulfill({
        json: {
          files: [
            'phenix-tunneler-darwin-amd64',
            'phenix-tunneler-darwin-arm64',
            'phenix-tunneler-linux-amd64',
            'phenix-tunneler-windows-amd64.exe',
          ],
        },
      }),
  );
  // the page has no page data to wait for
  await standIn(page);
  await page.goto('/tunneler');
  // The Quick Start box comes last and holds the docs button.
  await expect(
    page.getByRole('link', { name: 'Tunneler Documentation' }),
  ).toBeVisible();
  await expect(page.locator('a.tunneler-button')).toBeVisible();
  await expect(
    page.getByRole('link', { name: /phenix-tunneler-windows/ }),
  ).toBeVisible();
  await shootRegion(
    page,
    [page.locator('.navbar'), page.locator('#quick-start')],
    'tunneler_page',
    0,
  );
});

// The WebShark page on site-1-hmi's live capture before it records a packet:
// the stand-in turns on the webshark feature, and /webshark/watch gives a
// size of 24 bytes, a pcap header alone. The window is short, so the frame
// has its smallest height.
test('webshark_empty', async ({ page }) => {
  await page.setViewportSize({ width: 1600, height: 500 });
  await page.route(
    (url) => url.pathname.endsWith('/features'),
    (route) => route.fulfill({ json: { features: ['webshark'] } }),
  );
  await page.route(
    (url) => url.pathname.endsWith('/webshark/watch'),
    (route) =>
      route.fulfill({
        status: 200,
        headers: { 'content-type': 'text/event-stream' },
        body: 'event: capture-changed\ndata: {"size":24}\n\n',
      }),
  );
  // the page has no page data to wait for
  await standIn(page);
  await page.goto('/packets?exp=bennu&vm=site-1-hmi&iface=0');
  const frame = page.locator('.webshark-frame');
  await expect(frame.locator('.webshark-empty-text')).toHaveText(
    /^No packets yet\./,
  );
  await expect(page.locator('.webshark-spinner')).toHaveCount(0);
  await expect(
    page.getByRole('link', { name: 'Open the bennu experiment page' }),
  ).toBeVisible();
  await expect(page.getByRole('combobox', { name: 'Capture' })).toHaveValue(
    'live:0:site-1-hmi',
  );
  await shootRegion(
    page,
    [page.locator('.webshark-bar'), frame],
    'webshark_empty',
    0,
  );
});

// The Schedule menu of the stopped grid-training experiment, open beside its
// start button, with the server's schedulers.
test('schedule', async ({ page }) => {
  await open(page, '/experiment/grid-training');
  await page.getByRole('button', { name: 'Schedule VMs' }).click();
  const menu = page.locator('.schedule-menu .dropdown-content');
  await expect(menu.getByText('9 of 9 VMs have a host')).toBeVisible();
  await expect(menu.getByText('subnet-compute')).toBeVisible();
  await shootRegion(
    page,
    [page.getByRole('button', { name: 'Start experiment' }), menu],
    'schedule',
    16,
  );
});

// The SCORCH runs page of ics-water-plant: its totals, its buttons, and the
// baseline run above its pipeline.
test('scorch_runs_page', async ({ page }) => {
  await open(page, '/scorch/ics-water-plant');
  const run = page.locator('.scorch-run').first();
  await expect(run.getByText('Run: baseline')).toBeVisible();
  await expect(
    run.getByRole('button', { name: 'Download the artifacts of run baseline' }),
  ).toBeEnabled();
  await shootRegion(
    page,
    [page.locator('.runs-totals'), page.locator('.runs-header'), run],
    'scorch_runs_page',
    0,
  );
});

test('delete_exp', async ({ page }) => {
  await open(page, '/experiments');
  await page
    .getByRole('button', { name: 'Delete experiment grid-training' })
    .click();
  const dialog = page
    .locator('.dialog .modal-card')
    .filter({ hasText: 'Delete the Experiment' });
  await expect(dialog).toBeVisible();
  await shoot(dialog, 'delete_exp');
});

// Opens the start dialog of the stopped grid-training experiment from its
// row on the Experiments page.
async function startDialog(page) {
  await open(page, '/experiments');
  await page
    .getByRole('button', {
      name: 'Start experiment grid-training',
      exact: true,
    })
    .click();
  const dialog = page
    .locator('.dialog .modal-card')
    .filter({ hasText: 'Start the Experiment' });
  await expect(dialog).toBeVisible();
  return dialog;
}

// The start dialog with its options at their defaults, once Start has focus.
test('experiment_start_dialog', async ({ page }) => {
  const dialog = await startDialog(page);
  await expect(dialog.getByLabel('VLAN range maximum')).toBeVisible();
  await expect(
    dialog.getByRole('button', { name: 'Start', exact: true }),
  ).toBeFocused();
  await shoot(dialog, 'experiment_start_dialog');
});

// A start refused by the disk check: the sample answers grid-training's start
// with the broken_disks error.
test('exp_start_broken_disks', async ({ page }) => {
  const dialog = await startDialog(page);
  await dialog.getByRole('button', { name: 'Start', exact: true }).click();
  const notice = page
    .locator('.notification')
    .filter({ hasText: 'qemu cannot open 2 disks on 2 VMs' });
  await expect(notice).toBeVisible();
  await shoot(notice, 'exp_start_broken_disks');
});

// The Disks table's row for an image, by the name the Name column shows.
const diskRow = (page, label) =>
  page.locator('.b-table tbody tr').filter({
    has: page.locator('.disk-label', {
      hasText: new RegExp(`^${label.replace(/[.]/g, '\\.')}$`),
    }),
  });

// The visible tooltip that holds text, wherever the page put it.
const tooltip = (page, text) =>
  page.locator('.tooltip-content').filter({ hasText: text, visible: true });

// The warning on an image outside the minimega files directory.
test('disks_outside', async ({ page }) => {
  await open(page, '/disks');
  const name = diskRow(page, '/data/vms/win10-golden.qcow2').locator(
    'td[data-label="Name"]',
  );
  await name
    .getByRole('button', { name: 'Outside the standard images directory' })
    .hover();
  const tip = tooltip(page, 'minimega does not copy this image');
  await expect(tip).toBeVisible();
  await shootCells(firstTable(page), [name, tip], 'disks_outside');
});

// The In Use marker of an image a running VM holds open, with its tooltip.
test('disks_in_use', async ({ page }) => {
  await open(page, '/disks');
  const inUse = diskRow(page, 'bennu-hmi.qc2').locator(
    'td[data-label="In Use"]',
  );
  await inUse.locator('.icon').hover();
  const tip = tooltip(page, 'In use by bennu');
  await expect(tip).toBeVisible();
  const heading = page.locator('.b-table thead th').first();
  await shootCells(firstTable(page), [heading, inUse, tip], 'disks_in_use');
});

// Opens the details card of the image the Name column shows as label.
async function diskCard(page, label) {
  await page.setViewportSize({ width: 1280, height: 1600 });
  await open(page, '/disks');
  await page
    .locator('.disk-label', {
      hasText: new RegExp(`^${label.replace(/[.]/g, '\\.')}$`),
    })
    .click();
  const card = page.locator('.modal-card.disk-card');
  await expect(card).toBeVisible();
  return card;
}

// The details card's actions for an image no VM uses, all of them enabled.
test('disks_actions', async ({ page }) => {
  const card = await diskCard(page, 'ubuntu-22.04-dev.qc2');
  const actions = card.locator('.disk-section.actions');
  await expect(actions.locator('.disk-action-rebase')).toBeVisible();
  await shoot(actions, 'disks_actions');
});

// The Broken chain tag of an image whose backing file is missing, with its
// tooltip.
test('disks_broken_chain', async ({ page }) => {
  await open(page, '/disks');
  const name = diskRow(page, 'kali-lab.qc2').locator('td[data-label="Name"]');
  await name.locator('.broken-tag').hover();
  const tip = tooltip(page, 'ro_kali.qc2 is missing');
  await expect(tip).toBeVisible();
  await shootCells(firstTable(page), [name, tip], 'disks_broken_chain');
});

// The details card's backing chain of that image, ending in the missing file.
test('disks_broken_chain_card', async ({ page }) => {
  const card = await diskCard(page, 'kali-lab.qc2');
  const chain = card.locator('.disk-chain-section');
  await expect(chain.getByText('(missing)')).toBeVisible();
  await shoot(chain, 'disks_broken_chain_card');
});

// GET /vlans and GET /vlans/{id} from the sample cluster that the VLANs
// page's tests use, which the Go tests write from vlan.Merge.
function vlansAnswers(id) {
  return {
    'GET /vlans': vlansCluster,
    ...(id && {
      [`GET /vlans/${id}`]: {
        id,
        vlans: vlansCluster.vlans.filter((row) => row.id === id),
        vms: [],
        issues: vlansCluster.issues.filter((issue) => issue.vlan === id),
      },
    }),
  };
}

// The By experiment view: the totals, the issue banner, the filters and a
// card for each owner. The window is tall enough for every card.
test('vlans_cards', async ({ page }) => {
  await page.setViewportSize({ width: 1600, height: 1400 });
  await open(page, '/vlans', vlansAnswers());
  await expect(page.locator('.vlans-banner')).toContainText(
    '7 issues: 3 conflicts, 2 differences, 2 warnings.',
  );
  await expect(
    page.locator('[data-card="experiment:substation-a"]'),
  ).toBeVisible();
  await expect(page.locator('[data-card="blacklist"]')).toHaveCount(0);
  await shoot(page.locator('.vlans-page'), 'vlans_cards');
});

// The Blacklist view: the blacklist card alone, with its remove buttons.
test('vlans_blacklist', async ({ page }) => {
  await page.setViewportSize({ width: 1600, height: 900 });
  await open(page, '/vlans?view=blacklist', vlansAnswers());
  await expect(page.locator('[data-card="blacklist"]')).toBeVisible();
  await shoot(page.locator('.vlans-page'), 'vlans_blacklist');
});

// The ID space view with the details of VLAN 102, which scratch and
// substation-a both pin.
test('vlans_grid', async ({ page }) => {
  await page.setViewportSize({ width: 1600, height: 1400 });
  await open(page, '/vlans?view=grid&vlan=102', vlansAnswers(102));
  const panel = page.getByRole('complementary', { name: 'VLAN 102' });
  await expect(panel).toContainText('Also pinned by');
  await expect(page.locator('.vlan-grid-status')).toHaveText(/^ID 102: /);
  await page.evaluate(() => document.activeElement?.blur());
  await shoot(page.locator('.vlans-page'), 'vlans_grid');
});
