// Form POSTs that work against an empty store: user create/delete,
// settings save, and the config viewer/editor (using a config the test
// creates and removes itself).
const { test, expect } = require('@playwright/test');
const { attachCapture, settle, fatalOf, gotoSeeded } = require('./helpers');

test('users: create and delete a user via modal', async ({ page }) => {
  const issues = [];
  attachCapture(page, issues);
  await gotoSeeded(page, '/users');
  await settle(page);

  // clean leftover from previous runs
  await page.request.delete('/api/v1/users/e2e-user').catch(() => {});

  await page.locator('p.control button.button.is-light').click();
  await expect(page.getByText('Create a New User', { exact: true })).toBeVisible();

  const modal = page.locator('.modal-card');
  await modal.locator('input[type="text"]').nth(0).fill('e2e-user');
  await modal.locator('input[type="text"]').nth(1).fill('E2E');
  await modal.locator('input[type="text"]').nth(2).fill('User');
  await modal.locator('input[type="password"]').nth(0).fill('Testpass1!');
  await modal.locator('input[type="password"]').nth(1).fill('Testpass1!');
  await modal.locator('select').selectOption('Global Viewer');

  await page.getByRole('button', { name: 'Create User' }).click();
  // success path must close the modal
  await expect(page.locator('.modal-card')).toBeHidden({ timeout: 15000 });
  const row = page.locator('tr', { hasText: 'e2e-user' });
  await expect(row).toBeVisible({ timeout: 15000 });

  // delete it again via the trash button in its row (icons are inline SVGs)
  await row.locator('button:has(svg[data-icon="trash"])').click();
  const confirmBtn = page.getByRole('button', { name: 'Delete', exact: true });
  if (await confirmBtn.isVisible({ timeout: 3000 }).catch(() => false)) {
    await confirmBtn.click();
  }
  await expect(page.locator('tr', { hasText: 'e2e-user' })).toBeHidden({ timeout: 15000 });

  const fatal = fatalOf(issues);
  expect(fatal, JSON.stringify(fatal, null, 2)).toHaveLength(0);
});

test('settings: load and save round-trip', async ({ page }) => {
  const issues = [];
  attachCapture(page, issues);
  await gotoSeeded(page, '/settings');
  await settle(page);

  await page.getByRole('button', { name: 'Save Changes' }).click();
  await expect(page.getByText('Settings updated')).toBeVisible({ timeout: 10000 });

  const fatal = fatalOf(issues);
  expect(fatal, JSON.stringify(fatal, null, 2)).toHaveLength(0);
});

test('configs: view a config and open the editor', async ({ page }) => {
  const issues = [];
  attachCapture(page, issues);

  // self-contained fixture: a minimal Role config created via the API
  const fixture = {
    apiVersion: 'phenix.sandia.gov/v1',
    kind: 'Role',
    metadata: { name: 'e2e-viewer-role' },
    spec: {
      roleName: 'e2e-viewer-role',
      policies: [{ resources: ['experiments'], resourceNames: ['*'], verbs: ['list'] }],
    },
  };
  await page.request.delete('/api/v1/configs/role/e2e-viewer-role').catch(() => {});
  const created = await page.request.post('/api/v1/configs', { data: fixture });
  expect(created.ok(), await created.text()).toBeTruthy();

  await gotoSeeded(page, '/configs/');
  await settle(page);

  const row = page.locator('tr').filter({ hasText: 'e2e-viewer-role' });
  await row.getByText('e2e-viewer-role', { exact: true }).click();
  await settle(page, 1500);

  // viewer opens; Edit switches to the Ace-based editor
  await page.getByRole('button', { name: /edit/i }).first().click();
  await expect(page.locator('.ace_editor')).toBeVisible({ timeout: 20000 });

  await page.request.delete('/api/v1/configs/role/e2e-viewer-role').catch(() => {});

  const fatal = fatalOf(issues);
  expect(fatal, JSON.stringify(fatal, null, 2)).toHaveLength(0);
});
