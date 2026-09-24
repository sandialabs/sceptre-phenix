// Theme resolution: server default, browser-local override, and system
// preference. Needs a server whose default theme is not locked by
// `phenix ui --default-theme`; the suite skips itself otherwise.
const { test, expect } = require('@playwright/test');
const { gotoSeeded, settle } = require('./helpers');

let originalDefaultTheme = 'system';

async function setDefaultTheme(request, theme) {
  const response = await request.put('/api/v1/settings/theme', {
    data: { default_theme: theme },
  });
  expect(response.ok(), await response.text()).toBeTruthy();
}

test.beforeAll(async ({ request }) => {
  const response = await request.get('/api/v1/settings/theme');
  expect(response.ok(), await response.text()).toBeTruthy();
  const current = await response.json();
  test.skip(current.locked, 'default theme is locked by --default-theme');
  originalDefaultTheme = current.default_theme;
});

test.afterAll(async ({ request }) => {
  await setDefaultTheme(request, originalDefaultTheme);
});

test('theme: follows a dark system preference by default', async ({
  page,
  request,
}) => {
  await setDefaultTheme(request, 'system');
  await page.emulateMedia({ colorScheme: 'dark' });
  await gotoSeeded(page, '/experiments');

  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  const toggle = page.getByRole('button', { name: 'Switch to light mode' });
  await expect(toggle).toBeVisible();
  await expect(toggle.locator('svg')).toHaveAttribute('data-icon', 'moon');
});

test('theme: follows live system changes only in system mode', async ({
  page,
  request,
}) => {
  await setDefaultTheme(request, 'system');
  await page.emulateMedia({ colorScheme: 'dark' });
  await gotoSeeded(page, '/experiments');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');

  await page.emulateMedia({ colorScheme: 'light' });
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
});

test('theme: local choice overrides the global default and persists', async ({
  page,
  request,
}) => {
  await setDefaultTheme(request, 'light');
  await gotoSeeded(page, '/experiments');

  const toggle = page.getByRole('button', { name: 'Switch to dark mode' });
  await expect(toggle.locator('svg')).toHaveAttribute('data-icon', 'sun');
  await toggle.press('Enter');

  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await expect
    .poll(() => page.evaluate(() => localStorage.getItem('phenix.theme')))
    .toBe('dark');
  await page.reload();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await expect(
    page.getByRole('button', { name: 'Switch to light mode' }),
  ).toBeVisible();
});

test('theme: existing local choice wins over a dark global default', async ({
  page,
  request,
}) => {
  await setDefaultTheme(request, 'dark');
  await page.addInitScript(() => localStorage.setItem('phenix.theme', 'light'));
  await gotoSeeded(page, '/experiments');

  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
});

test('theme: Settings persists the shared global default', async ({
  page,
  request,
}) => {
  await setDefaultTheme(request, 'system');
  await gotoSeeded(page, '/settings');
  await settle(page);

  await page.locator('#default-theme').selectOption('dark');
  await page.getByRole('button', { name: 'Save Changes' }).click();
  await expect(page.getByText('Settings updated')).toBeVisible();

  const response = await request.get('/api/v1/settings/theme');
  expect(response.ok()).toBeTruthy();
  expect(await response.json()).toMatchObject({
    default_theme: 'dark',
    locked: false,
  });
});

test('theme: UI loads without external requests', async ({ page, baseURL }) => {
  const externalRequests = [];
  const expectedHost = new URL(baseURL).host;
  page.on('request', (browserRequest) => {
    const url = new URL(browserRequest.url());
    if (
      ['http:', 'https:', 'ws:', 'wss:'].includes(url.protocol) &&
      url.host !== expectedHost
    ) {
      externalRequests.push(browserRequest.url());
    }
  });

  await gotoSeeded(page, '/experiments');
  await settle(page);

  expect(externalRequests).toEqual([]);
});
