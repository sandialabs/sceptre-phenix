// Password-auth sign-in flows. Needs a VITE_AUTH=enabled UI and a server with a signing key and admin:
//   phenix ui --jwt-signing-key secret --users 'e2e-admin:Testpass1!:Global Admin'
//   E2E_AUTH_MODE=enabled npx playwright test auth-enabled
const { test, expect } = require('@playwright/test');
const {
  attachCapture,
  settle,
  fatalOf,
  expectTextContrast,
} = require('./helpers');

const ADMIN_USER = process.env.E2E_ADMIN_USER || 'e2e-admin';
const ADMIN_PASS = process.env.E2E_ADMIN_PASS || 'Testpass1!';
const SIGNUP_USER = 'e2e-signup';

test.skip(
  process.env.E2E_AUTH_MODE !== 'enabled',
  'set E2E_AUTH_MODE=enabled (see file header)',
);

// A user with the Disabled role may use no API, so the app's calls and its
// websocket can be refused once such a user signs in.
function filterDisabledRefusals(issues) {
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
  const fatal = fatalOf(issues);
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
    page.getByText('The username or password is incorrect'),
  ).toBeVisible({
    timeout: 10000,
  });
  const fatal = fatalOf(issues);
  expect(fatal, JSON.stringify(fatal, null, 2)).toHaveLength(0);
});

async function signIn(page) {
  await page.goto('/signin');
  await settle(page);
  await page.locator('.signin-form input[type="text"]').fill(ADMIN_USER);
  await page.locator('.signin-form input[type="password"]').fill(ADMIN_PASS);
  await page.getByRole('button', { name: 'Submit' }).click();
  await expect(page).toHaveURL(/\/experiments/, { timeout: 15000 });
}

test(
  'login lands on experiments',
  { tag: '@cross-browser' },
  async ({ page }) => {
    const issues = [];
    attachCapture(page, issues);
    await signIn(page);

    // Logout is a button the keyboard reaches. After a logout in the app,
    // which is no page load, typing starts in the Username field again.
    const logout = page.getByTestId('nav-logout');
    await expect(logout).toHaveRole('button');
    await logout.focus();
    await page.keyboard.press('Enter');
    await expect(page).toHaveURL(/\/signin/, { timeout: 15000 });
    await expect(
      page.locator('.signin-form').getByLabel('Username', { exact: true }),
    ).toBeFocused();

    const fatal = fatalOf(issues);
    expect(fatal, JSON.stringify(fatal, null, 2)).toHaveLength(0);
  },
);

test('signed-in pages load their data', async ({ page }) => {
  await signIn(page);
  // only what follows sign-in, which must all be authorized
  const issues = [];
  attachCapture(page, issues);

  for (const path of ['/experiments', '/configs/', '/users']) {
    await page.goto(path);
    await expect(
      page.locator('.refresh-label').filter({ hasText: 'Updated just now' }),
      path,
    ).toBeVisible();
  }

  const refused = issues.filter((i) => /^http-40[13]$/.test(i.kind));
  expect(refused, JSON.stringify(refused, null, 2)).toHaveLength(0);
  const fatal = fatalOf(issues);
  expect(fatal, JSON.stringify(fatal, null, 2)).toHaveLength(0);
});

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

  // Focus starts in the dialog's first field and returns to its opener. The
  // sign-in form's username is not copied into it.
  await signInName.fill(ADMIN_USER);
  await createAccount.click();
  await expect(dialog).toBeVisible();
  await expect(dialog).toHaveAttribute('aria-modal', 'true');
  await expect(field('User Name')).toBeFocused();
  await expect(field('User Name')).toHaveValue('');
  await expect(dialog.getByRole('button', { name: 'Close' })).toBeVisible();
  // A password too short shows a legible message once its field is left.
  for (const label of labels) {
    await field(label).fill('short');
  }
  await field('User Name').focus();
  await expect(dialog.locator('.help.is-danger').first()).toBeVisible();
  await expectTextContrast(
    page,
    '#signin .modal-card',
    'the Create a New Account dialog',
  );
  await page.keyboard.press('Escape');
  await expect(createAccount).toBeFocused();

  // Reopened while it is still closing, it is empty again and shows no
  // message. The sign-in form keeps its username.
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

  await createAccount.click();
  await expect(dialog).toBeVisible();
  await field('User Name').fill(ADMIN_USER);
  await field('First Name').fill('E2E');
  await field('Last Name').fill('Signup');
  await field('Password').fill('Testpass1!');
  await field('Confirm Password').fill('Testpass1!');

  // The admin's name is refused in the field, with focus, and in a toast.
  // Another name clears it.
  const createUser = dialog.getByRole('button', { name: 'Create User' });
  await createUser.click();
  const taken = dialog.getByText('User already exists');
  await expect(taken).toBeVisible();
  await expect(
    page.getByRole('alert').filter({ hasText: 'User already exists' }),
  ).toBeVisible();
  await expect(field('User Name')).toBeFocused();
  await field('User Name').fill(SIGNUP_USER);
  await expect(taken).toBeHidden();
  await createUser.click();

  // fresh self-signup users get the Disabled role until an admin assigns one
  await expect(page).toHaveURL(/\/disabled/, { timeout: 15000 });
  await expect(
    page.getByText('Your account is currently disabled'),
  ).toBeVisible();

  // The admin removes the account, so the test passes again on this server.
  expect(await deleteSignupUser(request)).toBe(204);

  const fatal = filterDisabledRefusals(fatalOf(issues));
  expect(fatal, JSON.stringify(fatal, null, 2)).toHaveLength(0);
});
