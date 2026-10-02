// Form POSTs that work against an empty store: user create/delete,
// settings save, and the config viewer/editor (using a config the test
// creates and removes itself).
const {
  test: base,
  expect,
  pageDataLoaded,
  roleConfig,
  createConfig,
  deleteConfig,
} = require('./helpers');

const test = base.extend({
  // a Role config to view, removed afterwards
  viewerRole: async ({ request }, use) => {
    const config = roleConfig('e2e-viewer-role');
    await createConfig(request, config);
    await use(config.metadata.name);
    await deleteConfig(request, config);
  },
});

test('users: create and delete a user via modal', async ({ page }) => {
  // clean leftover from previous runs
  await page.request.delete('/api/v1/users/e2e-user');

  await page.goto('/users');
  await pageDataLoaded(page);

  await page.getByRole('button', { name: 'Create a new user' }).click();
  await expect(
    page.getByText('Create a New User', { exact: true }),
  ).toBeVisible();

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
  await expect(page.locator('tr', { hasText: 'e2e-user' })).toBeVisible({
    timeout: 15000,
  });

  await page.getByRole('button', { name: 'Delete user e2e-user' }).click();
  await page.getByRole('button', { name: 'Delete', exact: true }).click();
  await expect(page.locator('tr', { hasText: 'e2e-user' })).toBeHidden({
    timeout: 15000,
  });
});

test('settings: load and save round-trip', async ({ page }) => {
  await page.goto('/settings');
  await pageDataLoaded(page);

  const save = page.getByRole('button', { name: 'Save Changes' });
  const toggle = page.locator('.switch', {
    hasText: 'Require a lowercase letter',
  });

  // Save only enables once something has changed
  await expect(save).toBeDisabled();
  await toggle.click();
  await save.click();
  await expect(page.getByText('Settings updated')).toBeVisible({
    timeout: 10000,
  });
  await expect(save).toBeDisabled();

  // put the setting back for the other tests
  await toggle.click();
  await save.click();
  await expect(save).toBeDisabled({ timeout: 10000 });
});

test('configs: view a config and open the editor', async ({
  page,
  viewerRole,
}) => {
  await page.goto('/configs/');
  await pageDataLoaded(page);

  const row = page.locator('tr').filter({ hasText: viewerRole });
  await row.getByText(viewerRole, { exact: true }).click();

  // viewer opens; Edit switches to the Ace-based editor
  await page.getByRole('button', { name: 'Edit Config', exact: true }).click();
  await expect(page.locator('.ace_editor')).toBeVisible({ timeout: 20000 });
});

test('configs: schema selection generates a config template', async ({
  page,
}) => {
  await page.goto('/configs/');
  await pageDataLoaded(page);

  await page.getByRole('button', { name: 'Create a new config' }).click();
  await expect(page.locator('.ace_editor')).toBeVisible({ timeout: 20000 });

  const fullSchema = page.waitForResponse(
    (response) =>
      response.url().endsWith('/api/v1/schemas/v2') &&
      response.request().method() === 'GET',
  );
  const roleSchema = page.waitForResponse(
    (response) =>
      response.url().endsWith('/api/v1/schemas/Role/v1') &&
      response.request().method() === 'GET',
  );

  await page
    .locator('.field.editor', { hasText: 'Config Kind' })
    .locator('select')
    .selectOption('Role');

  expect((await fullSchema).ok()).toBeTruthy();
  expect((await roleSchema).ok()).toBeTruthy();

  const editor = page.locator('.ace_content');
  await expect(editor).toContainText('kind: Role', { timeout: 20000 });
  await expect(editor).toContainText('roleName:');
  await expect(editor).toContainText('policies:');
});
