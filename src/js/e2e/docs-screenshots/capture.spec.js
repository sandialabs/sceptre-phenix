// Each test writes the docs image it is named after (`-g disks_table` retakes
// one) from the sample data in fixtures.json, so any `phenix ui` built with
// VITE_AUTH=disabled will do.
const path = require('node:path');
const { test, expect } = require('@playwright/test');
const { pageDataLoaded } = require('../tests/helpers');
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

test('header_refresh', async ({ page }) => {
  await open(page, '/hosts');
  await shoot(page.locator('.refresh-status'), 'header_refresh');
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
