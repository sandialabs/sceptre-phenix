// Full experiment lifecycle: create -> stopped view -> start -> running view
// -> stop -> delete. Requires a real phenix deployment (minimega, VM images,
// and a topology in the store), so it is opt-in:
//
//   E2E_LIFECYCLE=1 [E2E_TOPOLOGY=helloworld] npx playwright test experiment-lifecycle
const { test, expect } = require('@playwright/test');
const { attachCapture, settle, fatalOf, gotoSeeded } = require('./helpers');

const EXP = 'e2e-smoketest';
const TOPOLOGY = process.env.E2E_TOPOLOGY || 'helloworld';

test.skip(!process.env.E2E_LIFECYCLE, 'set E2E_LIFECYCLE=1 on a deployment with minimega + VM images');
test.describe.configure({ mode: 'serial' });

async function expState(page) {
  const r = await page.request.get('/api/v1/experiments');
  const j = await r.json();
  return (j.experiments || []).find((x) => x.name === EXP);
}

test.beforeAll(async ({ request }) => {
  await request.delete('/api/v1/experiments/' + EXP).catch(() => {});
});

test('create experiment via modal', async ({ page }) => {
  const issues = [];
  attachCapture(page, issues);
  await gotoSeeded(page, '/experiments');
  await settle(page);

  const createNow = page.getByRole('button', { name: 'Create One Now!' });
  if (await createNow.isVisible().catch(() => false)) {
    await createNow.click();
  } else {
    await page.locator('p.control button.button.is-light').click();
  }

  await expect(page.getByText('Create a New Experiment')).toBeVisible();
  await page.locator('.modal-card input[type="text"]').first().fill(EXP);
  await page.locator('.modal-card select').first().selectOption(TOPOLOGY);

  await page.getByRole('button', { name: 'Create Experiment' }).click();
  await expect(page.locator('.modal-card')).toBeHidden({ timeout: 60000 });
  await expect(page.getByRole('link', { name: EXP })).toBeVisible({ timeout: 60000 });
  expect(fatalOf(issues), JSON.stringify(fatalOf(issues), null, 2)).toHaveLength(0);
});

test('stopped experiment view renders', async ({ page }) => {
  const issues = [];
  attachCapture(page, issues);
  await gotoSeeded(page, '/experiment/' + EXP);
  await settle(page, 4000);
  expect(await page.locator('table tbody tr').count()).toBeGreaterThan(0);
  expect(fatalOf(issues), JSON.stringify(fatalOf(issues), null, 2)).toHaveLength(0);
});

test('start experiment', async ({ page }) => {
  test.setTimeout(420000);
  const issues = [];
  attachCapture(page, issues);
  await gotoSeeded(page, '/experiments');
  await settle(page);

  const row = page.locator('tr', { hasText: EXP });
  await row.locator('span.tag .field.is-clickable').click();
  await expect(page.getByText('Start the Experiment')).toBeVisible();
  await page.getByRole('button', { name: 'Start', exact: true }).click();

  await expect
    .poll(async () => ((await expState(page)) || {}).running, { timeout: 360000, intervals: [5000] })
    .toBe(true);
  expect(fatalOf(issues), JSON.stringify(fatalOf(issues), null, 2)).toHaveLength(0);
});

test('running experiment view renders', async ({ page }) => {
  const issues = [];
  attachCapture(page, issues);
  await gotoSeeded(page, '/experiment/' + EXP);
  await settle(page, 6000);
  expect(await page.locator('table tbody tr').count()).toBeGreaterThan(0);
  expect(fatalOf(issues), JSON.stringify(fatalOf(issues), null, 2)).toHaveLength(0);
});

test('stop experiment', async ({ page }) => {
  test.setTimeout(420000);
  const issues = [];
  attachCapture(page, issues);
  await gotoSeeded(page, '/experiments');
  await settle(page);

  const row = page.locator('tr', { hasText: EXP });
  await row.locator('span.tag .field.is-clickable').click();
  await expect(page.getByText('Stop the Experiment')).toBeVisible();
  await page.getByRole('button', { name: 'Stop', exact: true }).click();

  await expect
    .poll(async () => ((await expState(page)) || {}).running, { timeout: 360000, intervals: [5000] })
    .toBe(false);
  expect(fatalOf(issues), JSON.stringify(fatalOf(issues), null, 2)).toHaveLength(0);
});

test('delete experiment', async ({ page }) => {
  const issues = [];
  attachCapture(page, issues);
  await gotoSeeded(page, '/experiments');
  await settle(page);

  const row = page.locator('tr', { hasText: EXP });
  await row.locator('.action button').first().click();
  await expect(page.getByText('Delete the Experiment')).toBeVisible();
  await page.getByRole('button', { name: 'Delete', exact: true }).click();

  await expect
    .poll(async () => !!(await expState(page)), { timeout: 60000, intervals: [3000] })
    .toBe(false);
  expect(fatalOf(issues), JSON.stringify(fatalOf(issues), null, 2)).toHaveLength(0);
});
