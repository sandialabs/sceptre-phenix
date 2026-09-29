// Password-auth (enabled mode) sign-in flows. Opt-in: needs the UI built with
// VITE_AUTH=enabled and the server started with a signing key and a known
// admin, e.g.:
//
//   phenix ui --jwt-signing-key secret --users 'e2e-admin:Testpass1!:Global Admin'
//   E2E_AUTH_MODE=enabled npx playwright test auth-enabled
//
// The create account test deletes its e2e-signup account as that admin
// (E2E_ADMIN_USER and E2E_ADMIN_PASS), so it can run again on the same server.
const { test, expect } = require('@playwright/test');
const { attachCapture, settle, fatalOf } = require('./helpers');

const ADMIN_USER = process.env.E2E_ADMIN_USER || 'e2e-admin';
const ADMIN_PASS = process.env.E2E_ADMIN_PASS || 'Testpass1!';
const SIGNUP_USER = 'e2e-signup';

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

// Deletes the account the signup test makes. Only the admin may: the server
// refuses a signed-out request. Returns the response's status.
async function deleteSignupUser(request) {
  const login = await request.post('/api/v1/login', {
    data: { user: ADMIN_USER, pass: ADMIN_PASS },
  });
  expect(login.ok(), await login.text()).toBeTruthy();
  const { token } = await login.json();
  const removed = await request.delete(`/api/v1/users/${SIGNUP_USER}`, {
    headers: { 'X-Phenix-Auth-Token': `bearer ${token}` },
  });

  return removed.status();
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
  request,
}) => {
  const issues = [];
  attachCapture(page, issues);
  // Left behind only by a run that failed before its end.
  await deleteSignupUser(request);
  await page.goto('/signin');
  await settle(page);

  const signInName = page
    .locator('.signin-form')
    .getByLabel('Username', { exact: true });
  const createAccount = page.getByRole('button', { name: 'Create Account' });
  const dialog = page.getByRole('dialog', { name: 'Create a New Account' });
  const modal = page.locator('#signin .modal');
  const field = (label) => dialog.getByLabel(label, { exact: true });
  const labels = [
    'User Name',
    'First Name',
    'Last Name',
    'Password',
    'Confirm Password',
  ];

  // The dialog is named, focus starts in its first field, and closing it
  // puts focus back on the button that opened it. Its fields are its own:
  // the sign-in form's username is not copied into it.
  await signInName.fill(ADMIN_USER);
  await createAccount.click();
  await expect(dialog).toBeVisible();
  await expect(dialog).toHaveAttribute('aria-modal', 'true');
  await expect(field('User Name')).toBeFocused();
  await expect(field('User Name')).toHaveValue('');
  await expect(dialog.getByRole('button', { name: 'Close' })).toBeVisible();
  // A password too short shows a message once its field is left.
  for (const label of labels) {
    await field(label).fill('short');
  }
  await field('User Name').focus();
  await expect(dialog.locator('.help.is-danger').first()).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(createAccount).toBeFocused();

  // Reopened while it is still closing, it is empty again, and shows no
  // message; the sign-in form keeps its username.
  await createAccount.click();
  await expect(dialog).toBeVisible();
  await expect(field('User Name')).toBeFocused();
  for (const label of labels) {
    await expect.soft(field(label), label).toHaveValue('');
  }
  await expect.soft(dialog.locator('.help.is-danger')).toHaveCount(0);
  await expect.soft(signInName).toHaveValue(ADMIN_USER);

  // Reopened the moment it has finished closing, it shows, and closes.
  await page.keyboard.press('Escape');
  await page.waitForFunction(
    () => {
      const hidden = document.querySelector('#signin .modal');
      return !hidden || getComputedStyle(hidden).display === 'none';
    },
    null,
    { polling: 'raf', timeout: 5000 },
  );
  await createAccount.click();
  await expect(modal).toHaveCSS('opacity', '1');
  await page.keyboard.press('Escape');
  await expect(dialog).toBeHidden();

  // The admin's name is refused: the dialog stays open, its User Name field
  // says so and has focus, and a toast announces it. Another name clears it.
  await createAccount.click();
  await expect(dialog).toBeVisible();
  await field('User Name').fill(ADMIN_USER);
  await field('First Name').fill('E2E');
  await field('Last Name').fill('Signup');
  await field('Password').fill('Testpass1!');
  await field('Confirm Password').fill('Testpass1!');
  await dialog.getByRole('button', { name: 'Create User' }).click();
  const taken = dialog.getByText('User already exists');
  await expect(taken).toBeVisible();
  await expect(
    page.getByRole('alert').filter({ hasText: 'User already exists' }),
  ).toBeVisible();
  await expect(field('User Name')).toBeFocused();
  await field('User Name').fill(SIGNUP_USER);
  await expect(taken).toBeHidden();
  await dialog.getByRole('button', { name: 'Create User' }).click();

  // fresh self-signup users get the Disabled role until an admin assigns one
  await expect(page).toHaveURL(/\/disabled/, { timeout: 15000 });
  await expect(
    page.getByText('Your account is currently disabled'),
  ).toBeVisible();

  // The admin removes the account, so the test passes again on this server.
  expect(await deleteSignupUser(request)).toBe(204);

  const fatal = filterKnown403s(fatalOf(issues));
  expect(fatal, JSON.stringify(fatal, null, 2)).toHaveLength(0);
});
