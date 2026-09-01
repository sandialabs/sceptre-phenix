const { test, expect } = require('@playwright/test');
const AxeBuilder = require('@axe-core/playwright').default;

const { attachCapture, fatalOf, gotoSeeded } = require('./helpers');

function topologyName(testInfo, suffix) {
  const nonce = `${Date.now()}-${Math.floor(Math.random() * 100000)}`;

  return `builder-${testInfo.project.name}-${suffix}-${nonce}`;
}

async function expectAccessible(page) {
  const results = await new AxeBuilder({ page })
    .include('.builder-root')
    .withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'])
    .analyze();
  const serious = results.violations.filter((item) =>
    ['serious', 'critical'].includes(item.impact),
  );

  expect(serious, JSON.stringify(serious, null, 2)).toEqual([]);
}

async function openBuilder(page) {
  await gotoSeeded(page, '/builder-beta');
  await expect(page.getByRole('heading', { name: 'Builder Beta' })).toBeVisible(
    {
      timeout: 20000,
    },
  );
}

test('blank diagram autosaves, reloads, and supports keyboard editing', async ({
  page,
}) => {
  const issues = [];
  attachCapture(page, issues);
  await openBuilder(page);
  await expectAccessible(page);

  const createResponse = page.waitForResponse(
    (response) =>
      response.request().method() === 'POST' &&
      new URL(response.url()).pathname.endsWith('/api/v1/builder/drafts'),
  );
  await page.getByTestId('drafts-blank').click();
  const created = await (await createResponse).json();

  await expect(page.getByTestId('builder-canvas')).toBeVisible();
  const name = 'Browser edited topology';
  await page.getByTestId('builder-name').fill(name);
  await page.getByTestId('builder-name').blur();

  await page.getByTestId('palette-device').click();
  await page.getByTestId('palette-switch').click();
  await expect(page.getByTestId('builder-summary')).toContainText(
    '1 devices, 1 switches',
  );

  await page.locator('#connect-device').selectOption({ index: 1 });
  await page.locator('#connect-switch').selectOption({ index: 1 });
  await page.getByTestId('outline-connect').click();
  await expect(page.getByTestId('builder-summary')).toContainText(
    '1 connections',
  );
  await expect(page.getByTestId('builder-save-state')).toContainText(
    'All changes saved',
    { timeout: 20000 },
  );

  await page.getByTestId('toolbar-theme').click();
  await expect
    .poll(() =>
      page.evaluate(() => localStorage.getItem('phenix.builder.theme')),
    )
    .not.toBe('system');
  await expectAccessible(page);

  await page.reload();
  await expect(page.getByRole('heading', { name: 'Builder Beta' })).toBeVisible(
    {
      timeout: 20000,
    },
  );
  await page.getByTestId(`draft-open-${created.id}`).click();
  await expect(page.getByTestId('builder-name')).toHaveValue(name);
  await expect(page.getByTestId('builder-summary')).toContainText(
    '1 connections',
  );

  const fatal = fatalOf(issues);
  expect(fatal, JSON.stringify(fatal, null, 2)).toHaveLength(0);
});

test('publishes and reopens an immutable Builder document', async ({
  page,
  request,
}, testInfo) => {
  const issues = [];
  attachCapture(page, issues);
  await openBuilder(page);
  await page.getByTestId('drafts-blank').click();
  await expect(page.getByTestId('builder-save-state')).toContainText(
    'All changes saved',
    { timeout: 20000 },
  );

  const target = topologyName(testInfo, 'publish');
  await page.getByTestId('toolbar-publish').click();
  await page.getByTestId('publish-name').fill(target);
  await expectAccessible(page);
  await page.getByTestId('publish-submit').click();
  await expect(page.getByTestId('publish-result')).toContainText(
    'Every stage succeeded',
    { timeout: 20000 },
  );

  const config = await request.get(`/api/v1/configs/Topology/${target}`, {
    headers: { Accept: 'application/json' },
  });
  expect(config.ok(), await config.text()).toBeTruthy();
  expect(
    (await config.json()).metadata.annotations['builder-doc'],
  ).toBeTruthy();

  await page.getByRole('button', { name: 'Close', exact: true }).click();
  await page.getByRole('button', { name: 'Back to drafts' }).click();
  await page.getByTestId('drafts-tab-published').click();
  const card = page.locator('[data-testid="drafts-list-published"] li', {
    hasText: target,
  });
  await expect(card).toBeVisible();
  await card.getByRole('button', { name: `Open ${target}` }).click();
  await expect(page.getByTestId('builder-canvas')).toBeVisible();
  await expect(page.getByTestId('builder-save-state')).toContainText(
    'All changes saved',
    { timeout: 20000 },
  );

  await page.goto('/configs/');
  const configRow = page.locator('tr', { hasText: target });
  await expect(configRow).toContainText('builder beta');
  await configRow.locator('button.action').first().click();
  await expect(page).toHaveURL(
    new RegExp(`/builder-beta\\?topology=${encodeURIComponent(target)}`),
  );
  await expect(page.getByTestId('builder-canvas')).toBeVisible();

  const deleted = await request.delete(`/api/v1/configs/Topology/${target}`);
  expect([200, 204]).toContain(deleted.status());

  const fatal = fatalOf(issues);
  expect(fatal, JSON.stringify(fatal, null, 2)).toHaveLength(0);
});

test('generates a draft from an uploaded topology config', async ({
  page,
}, testInfo) => {
  const issues = [];
  attachCapture(page, issues);
  await openBuilder(page);

  const name = topologyName(testInfo, 'upload');
  const content = [
    'apiVersion: phenix.sandia.gov/v1',
    'kind: Topology',
    'metadata:',
    `  name: ${name}`,
    'spec:',
    '  nodes: []',
    '',
  ].join('\n');

  await page.getByTestId('drafts-generate').click();
  await page.getByLabel('Uploaded config').check();
  await page.getByTestId('generate-file').setInputFiles({
    name: 'topology.yaml',
    mimeType: 'application/yaml',
    buffer: Buffer.from(content),
  });
  await page.getByTestId('generate-submit').click();

  await expect(page.getByTestId('builder-name')).toHaveValue(name, {
    timeout: 20000,
  });
  await expect(page.getByTestId('builder-save-state')).toContainText(
    'All changes saved',
    { timeout: 20000 },
  );
  await expectAccessible(page);

  const fatal = fatalOf(issues);
  expect(fatal, JSON.stringify(fatal, null, 2)).toHaveLength(0);
});

test('protects legacy Builder configs from raw editing', async ({
  page,
  request,
}, testInfo) => {
  const issues = [];
  attachCapture(page, issues);

  const name = topologyName(testInfo, 'legacy');
  const created = await request.post('/api/v1/configs', {
    data: {
      apiVersion: 'phenix.sandia.gov/v1',
      kind: 'Topology',
      metadata: {
        name,
        annotations: { 'builder-xml': '<mxGraphModel />' },
      },
      spec: { nodes: [] },
    },
  });
  expect(created.ok(), await created.text()).toBeTruthy();

  await gotoSeeded(page, '/configs/');
  const configRow = page.locator('tr', { hasText: name });
  await expect(configRow).toContainText('builder legacy');
  await configRow.locator('button.action').first().click();

  await expect(
    page.getByText('This configuration can only be edited in Builder', {
      exact: true,
    }),
  ).toBeVisible();
  await expect(page).toHaveURL(/\/configs\/$/);
  await expect(page.getByRole('button', { name: 'Save' })).toBeDisabled();
  await page.getByRole('button', { name: 'OK' }).click();
  await expect(configRow).toBeVisible();

  const deleted = await request.delete(`/api/v1/configs/Topology/${name}`);
  expect([200, 204]).toContain(deleted.status());

  const fatal = fatalOf(issues);
  expect(fatal, JSON.stringify(fatal, null, 2)).toHaveLength(0);
});
