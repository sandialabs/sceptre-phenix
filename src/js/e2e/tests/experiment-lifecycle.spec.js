// Create, start, stop and delete an experiment; opt-in, as it needs minimega,
// VM images and a topology:
//   E2E_LIFECYCLE=1 [E2E_TOPOLOGY=helloworld] \
//     npx playwright test --no-deps experiment-lifecycle
const { test, expect } = require('@playwright/test');
const { attachCapture, settle, fatalOf } = require('./helpers');

const EXP = 'e2e-smoketest';
const TOPOLOGY = process.env.E2E_TOPOLOGY || 'helloworld';

test.skip(
  !process.env.E2E_LIFECYCLE,
  'set E2E_LIFECYCLE=1 on a deployment with minimega + VM images',
);
// a real deployment is slower than the CI server; start and stop wait longer
test.describe.configure({ mode: 'serial', timeout: 120000 });

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
  await page.goto('/experiments');
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
  await expect(page.getByRole('link', { name: EXP, exact: true })).toBeVisible({
    timeout: 60000,
  });
  expect(
    fatalOf(issues),
    JSON.stringify(fatalOf(issues), null, 2),
  ).toHaveLength(0);
});

test('stopped experiment view renders', async ({ page }) => {
  const issues = [];
  attachCapture(page, issues);
  await page.goto('/experiment/' + EXP);
  await settle(page, 4000);
  expect(await page.locator('table tbody tr').count()).toBeGreaterThan(0);
  expect(
    fatalOf(issues),
    JSON.stringify(fatalOf(issues), null, 2),
  ).toHaveLength(0);
});

test('start experiment', async ({ page }) => {
  test.setTimeout(420000);
  const issues = [];
  attachCapture(page, issues);
  await page.goto('/experiments');
  await settle(page);

  await page
    .getByRole('button', {
      name: `Start experiment ${EXP}: stopped`,
      exact: true,
    })
    .click();
  await page.locator('.dialog footer .button', { hasText: 'Start' }).click();

  await expect
    .poll(async () => ((await expState(page)) || {}).running, {
      timeout: 360000,
      intervals: [5000],
    })
    .toBe(true);
  expect(
    fatalOf(issues),
    JSON.stringify(fatalOf(issues), null, 2),
  ).toHaveLength(0);
});

test('running experiment view renders', async ({ page }) => {
  const issues = [];
  attachCapture(page, issues);
  await page.goto('/experiment/' + EXP);
  await settle(page, 6000);
  expect(await page.locator('table tbody tr').count()).toBeGreaterThan(0);
  expect(
    fatalOf(issues),
    JSON.stringify(fatalOf(issues), null, 2),
  ).toHaveLength(0);
});

test('restart experiment from the table', async ({ page }) => {
  test.setTimeout(840000);
  const issues = [];
  attachCapture(page, issues);
  await page.goto('/experiments');
  await settle(page);

  await page
    .getByRole('button', { name: `Restart experiment ${EXP}`, exact: true })
    .click();
  await expect(page.getByText('Restart the Experiment')).toBeVisible();
  const dialog = page.locator('.dialog');
  for (const name of [
    'Keep the injection snapshots',
    'Force the stop',
    'Treat minimega errors as warnings',
  ]) {
    await expect(
      dialog.getByRole('checkbox', { name, exact: true }),
    ).not.toBeChecked();
  }
  await expect(
    dialog.getByRole('checkbox', {
      name: 'Dry run (do not call minimega)',
      exact: true,
    }),
  ).toHaveCount(0);

  const answer = page.waitForResponse(
    (r) =>
      r.request().method() === 'POST' &&
      r.url().endsWith(`/experiments/${EXP}/restart`),
    { timeout: 720000 },
  );
  await dialog.locator('footer .button', { hasText: 'Restart' }).click();
  expect((await answer).ok()).toBe(true);
  expect(((await expState(page)) || {}).running).toBe(true);
  expect(
    fatalOf(issues),
    JSON.stringify(fatalOf(issues), null, 2),
  ).toHaveLength(0);
});

test('restart experiment from its page returns to the running view', async ({
  page,
}) => {
  test.setTimeout(840000);
  const issues = [];
  attachCapture(page, issues);
  await page.goto('/experiment/' + EXP);
  await settle(page, 6000);

  await page
    .getByRole('button', { name: 'Restart experiment', exact: true })
    .click();
  await expect(page.getByText('Restart the Experiment')).toBeVisible();
  const answer = page.waitForResponse(
    (r) =>
      r.request().method() === 'POST' &&
      r.url().endsWith(`/experiments/${EXP}/restart`),
    { timeout: 720000 },
  );
  await page.locator('.dialog footer .button', { hasText: 'Restart' }).click();
  // the stopped view while it starts again, then the running view
  await expect(
    page.getByRole('button', { name: 'Start experiment', exact: true }),
  ).toBeVisible({ timeout: 360000 });
  expect((await answer).ok()).toBe(true);
  await expect(
    page.getByRole('button', { name: 'Restart experiment', exact: true }),
  ).toBeVisible({ timeout: 60000 });
  expect(await page.locator('table tbody tr').count()).toBeGreaterThan(0);
  expect(
    fatalOf(issues),
    JSON.stringify(fatalOf(issues), null, 2),
  ).toHaveLength(0);
});

test('stop experiment', async ({ page }) => {
  test.setTimeout(420000);
  const issues = [];
  attachCapture(page, issues);
  await page.goto('/experiments');
  await settle(page);

  await page
    .getByRole('button', {
      name: `Stop experiment ${EXP}: started`,
      exact: true,
    })
    .click();
  await expect(page.getByText('Stop the Experiment')).toBeVisible();
  // both options open unchecked
  for (const name of ['Keep the injection snapshots', 'Force the stop']) {
    await expect(
      page.locator('.dialog').getByRole('checkbox', { name, exact: true }),
    ).not.toBeChecked();
  }
  await page.getByRole('button', { name: 'Stop', exact: true }).click();

  await expect
    .poll(async () => ((await expState(page)) || {}).running, {
      timeout: 360000,
      intervals: [5000],
    })
    .toBe(false);
  expect(
    fatalOf(issues),
    JSON.stringify(fatalOf(issues), null, 2),
  ).toHaveLength(0);
});

test('delete experiment', async ({ page }) => {
  const issues = [];
  attachCapture(page, issues);
  await page.goto('/experiments');
  await settle(page);

  const row = page.locator('tr', { hasText: EXP });
  await row.locator('.action button').first().click();
  await expect(page.getByText('Delete the Experiment')).toBeVisible();
  await page.getByRole('button', { name: 'Delete', exact: true }).click();

  await expect
    .poll(async () => !!(await expState(page)), {
      timeout: 60000,
      intervals: [3000],
    })
    .toBe(false);
  expect(
    fatalOf(issues),
    JSON.stringify(fatalOf(issues), null, 2),
  ).toHaveLength(0);
});
