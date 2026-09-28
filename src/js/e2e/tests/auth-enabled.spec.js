// Password-auth (enabled mode) sign-in flows. Opt-in: needs the UI built with
// VITE_AUTH=enabled and the server started with a signing key and a known
// admin, e.g.:
//
//   phenix ui --jwt-signing-key secret --users 'e2e-admin:Testpass1!:Global Admin'
//   E2E_AUTH_MODE=enabled npx playwright test auth-enabled
const { test, expect } = require('@playwright/test');
const { attachCapture, settle, fatalOf } = require('./helpers');

const ADMIN_USER = process.env.E2E_ADMIN_USER || 'e2e-admin';
const ADMIN_PASS = process.env.E2E_ADMIN_PASS || 'Testpass1!';

test.skip(
  process.env.E2E_AUTH_MODE !== 'enabled',
  'set E2E_AUTH_MODE=enabled (see file header)',
);

// Known upstream issue (predates the Vue 3 upgrade): in enabled mode the jwt
// middleware stores the parsed token under a plain-string context key while
// userMiddleware reads a typed key, so authenticated API calls and the
// websocket return 403 even after a successful login. The login/signup routes
// themselves bypass that check and are what these tests cover.
function filterKnown403s(issues) {
  return issues.filter(
    (i) =>
      !/403/.test(i.text) && !/WebSocket connection .* failed/.test(i.text),
  );
}

test('unauthenticated visit redirects to signin', async ({ page }) => {
  const issues = [];
  attachCapture(page, issues);
  await page.goto('/');
  await settle(page);
  expect(new URL(page.url()).pathname).toBe('/signin');
  await expect(page.getByRole('button', { name: 'Submit' })).toBeVisible();
  const fatal = filterKnown403s(fatalOf(issues));
  expect(fatal, JSON.stringify(fatal, null, 2)).toHaveLength(0);
});

test('wrong password shows incorrect-credentials toast', async ({ page }) => {
  const issues = [];
  attachCapture(page, issues);
  await page.goto('/signin');
  await settle(page);
  await page.locator('.signin-form input[type="text"]').fill(ADMIN_USER);
  await page
    .locator('.signin-form input[type="password"]')
    .fill('definitely-wrong');
  await page.getByRole('button', { name: 'Submit' }).click();
  await expect(
    page.getByText('The username and/or password is incorrect'),
  ).toBeVisible({
    timeout: 10000,
  });
  const fatal = filterKnown403s(fatalOf(issues));
  expect(fatal, JSON.stringify(fatal, null, 2)).toHaveLength(0);
});

test(
  'login lands on experiments',
  { tag: '@cross-browser' },
  async ({ page }) => {
    const issues = [];
    attachCapture(page, issues);
    await page.goto('/signin');
    await settle(page);
    await page.locator('.signin-form input[type="text"]').fill(ADMIN_USER);
    await page.locator('.signin-form input[type="password"]').fill(ADMIN_PASS);
    await page.getByRole('button', { name: 'Submit' }).click();
    await expect(page).toHaveURL(/\/experiments/, { timeout: 15000 });

    // After a logout in the app, which is no page load, typing starts in the
    // Username field again.
    await page.getByTestId('nav-logout').click();
    await expect(page).toHaveURL(/\/signin/, { timeout: 15000 });
    await expect(
      page.locator('.signin-form').getByLabel('Username', { exact: true }),
    ).toBeFocused();

    const fatal = filterKnown403s(fatalOf(issues));
    expect(fatal, JSON.stringify(fatal, null, 2)).toHaveLength(0);
  },
);

test('create account via signup modal lands on disabled page', async ({
  page,
}) => {
  const issues = [];
  attachCapture(page, issues);
  await page.request.delete('/api/v1/users/e2e-signup').catch(() => {});
  await page.goto('/signin');
  await settle(page);

  const createAccount = page.getByRole('button', { name: 'Create Account' });
  const dialog = page.getByRole('dialog', { name: 'Create a New Account' });

  // The dialog is named, focus starts in its first field, and closing it
  // puts focus back on the button that opened it.
  await createAccount.click();
  await expect(dialog).toBeVisible();
  await expect(dialog).toHaveAttribute('aria-modal', 'true');
  await expect(dialog.getByLabel('User Name', { exact: true })).toBeFocused();
  await expect(dialog.getByRole('button', { name: 'Close' })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(createAccount).toBeFocused();
  // Reopened while it closes, the dialog keeps stale fields: its content
  // goes only once it has closed.
  await expect(
    page.getByText('Create a New Account', { exact: true }),
  ).toHaveCount(0);

  await createAccount.click();
  await expect(dialog).toBeVisible();
  await expect(dialog.getByLabel('User Name', { exact: true })).toBeFocused();

  const modal = page.locator('.modal-card');
  await modal.getByLabel('User Name', { exact: true }).fill('e2e-signup');
  await modal.getByLabel('First Name', { exact: true }).fill('E2E');
  await modal.getByLabel('Last Name', { exact: true }).fill('Signup');
  await modal.getByLabel('Password', { exact: true }).fill('Testpass1!');
  await modal
    .getByLabel('Confirm Password', { exact: true })
    .fill('Testpass1!');
  await page.getByRole('button', { name: 'Create User' }).click();

  // fresh self-signup users get the Disabled role until an admin assigns one
  await expect(page).toHaveURL(/\/disabled/, { timeout: 15000 });
  await expect(
    page.getByText('Your account is currently disabled'),
  ).toBeVisible();

  const fatal = filterKnown403s(fatalOf(issues));
  expect(fatal, JSON.stringify(fatal, null, 2)).toHaveLength(0);
});
