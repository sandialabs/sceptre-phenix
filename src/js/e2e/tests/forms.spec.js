// Form POSTs that work against an empty store: user create/delete, settings
// save, and the config template that the server's schemas give.
const {
  test,
  expect,
  pageDataLoaded,
  expectTextContrast,
  visit,
} = require('./helpers');

// Opens the Create a New User dialog on the Users page and fills it in.
async function fillCreateUser(page, username) {
  await visit(page, '/users');
  await pageDataLoaded(page);

  await page.getByRole('button', { name: 'Create a new user' }).click();
  const modal = page.locator('.modal-card');
  await modal.locator('input[type="text"]').nth(0).fill(username);
  await modal.locator('input[type="text"]').nth(1).fill('E2E');
  await modal.locator('input[type="text"]').nth(2).fill('User');
  await modal.locator('input[type="password"]').nth(0).fill('Testpass1!');
  await modal.locator('input[type="password"]').nth(1).fill('Testpass1!');
  await modal.locator('select').selectOption('Global Viewer');
  return modal;
}

test('users: create and delete a user via modal', async ({ page }) => {
  // clean leftover from previous runs
  await page.request.delete('/api/v1/users/e2e-user');

  const modal = await fillCreateUser(page, 'e2e-user');
  await page.getByRole('button', { name: 'Create User' }).click();
  // success path must close the modal
  await expect(modal).toBeHidden({ timeout: 15000 });
  await expect(page.locator('tr', { hasText: 'e2e-user' })).toBeVisible({
    timeout: 15000,
  });

  await page.getByRole('button', { name: 'Delete user e2e-user' }).click();
  await page.getByRole('button', { name: 'Delete', exact: true }).click();
  await expect(page.locator('tr', { hasText: 'e2e-user' })).toBeHidden({
    timeout: 15000,
  });
});

test(
  'users: the Create a New User dialog with a taken name',
  { tag: '@axe' },
  async ({ page }) => {
    const modal = await fillCreateUser(page, 'e2e-taken-user');
    await page.route('**/api/v1/users', (route) =>
      route.request().method() === 'POST'
        ? route.fulfill({
            status: 409,
            contentType: 'text/plain',
            body: 'user e2e-taken-user already exists\n',
          })
        : route.fallback(),
    );
    await page.getByRole('button', { name: 'Create User' }).click();
    await expect(modal.getByText('User already exists')).toBeVisible();
    await expectTextContrast(
      page,
      '.modal-card',
      'the Create a New User dialog',
    );
  },
);

test('settings: load and save round-trip', async ({ page }) => {
  await visit(page, '/settings');
  await pageDataLoaded(page);

  const save = page.getByRole('button', { name: 'Save Changes' });
  const toggle = page.locator('.switch', {
    hasText: 'Require a lowercase letter',
  });

  await toggle.click();
  await save.click();
  await expect(page.getByText('Settings updated')).toBeVisible({
    timeout: 10000,
  });

  // put the setting back for the other tests
  await toggle.click();
  await save.click();
  await expect(save).toBeDisabled({ timeout: 10000 });
});

test('configs: schema selection generates a config template', async ({
  page,
}) => {
  await visit(page, '/configs/');
  await pageDataLoaded(page);

  await page.getByRole('button', { name: 'Create a new config' }).click();
  await expect(page.locator('.ace_editor')).toBeVisible({ timeout: 20000 });

  const fullSchema = page.waitForResponse(
    (response) =>
      response.url().endsWith('/api/v1/schemas/v1') &&
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
