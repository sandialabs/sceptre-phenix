// Each test writes the docs image it is named after (`-g disks_table` retakes
// one) from the sample data in fixtures.json, so any `phenix ui` built with
// VITE_AUTH=disabled will do.
const path = require('node:path');
const { test, expect } = require('@playwright/test');
const { pageDataLoaded } = require('../tests/helpers');
const { COLORS, standInExperiment } = require('../tests/vnc-support');
const fixtures = require('./fixtures.json');

const IMAGES =
  process.env.DOCS_IMAGES_DIR ??
  path.resolve(__dirname, '../../../../docs/content/images');

const image = (name) => path.join(IMAGES, `${name}.png`);

// Answers the API requests named in fixtures.json, as "METHOD /path" under
// /api/v1, with their sample bodies; overrides replace or add bodies.
async function standIn(page, overrides = {}) {
  const responses = { ...fixtures, ...overrides };
  await page.route(
    (url) => url.pathname.includes('/api/v1/'),
    (route) => {
      const request = route.request();
      const apiPath = new URL(request.url()).pathname.replace(
        /^.*\/api\/v1/,
        '',
      );
      const body = responses[`${request.method()} ${apiPath}`];
      return body === undefined
        ? route.fallback()
        : route.fulfill({ json: body });
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

// Draws a red ring around a control, as the docs mark the one to click.
async function ring(locator) {
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

// The refresh button with its tooltip, which says when the data was updated.
test('header_refresh', async ({ page }) => {
  await open(page, '/hosts');
  const tip = page.locator('.refresh-status .tooltip-content');
  await page.locator('.refresh-status button').hover();
  await expect(tip).toBeVisible();
  await shootRegion(
    page,
    [page.locator('.refresh-status'), tip],
    'header_refresh',
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
  await open(page, '/experiments');
  await shoot(page.locator('nav.navbar'), 'navbar');
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
