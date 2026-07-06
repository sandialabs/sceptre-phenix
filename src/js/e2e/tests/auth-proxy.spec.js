// Proxy-auth signup flow. Opt-in: needs the UI built with VITE_AUTH=proxy and
// the server started with --jwt-signing-key proxy-jwt, e.g.:
//
//   phenix ui --jwt-signing-key proxy-jwt
//   E2E_AUTH_MODE=proxy npx playwright test auth-proxy
//
// The auth proxy is simulated with Playwright's extraHTTPHeaders: every
// request carries the proxy-style JWT, which the server intentionally parses
// without verifying (that is what proxy mode means).
const { test, expect } = require('@playwright/test');
const { attachCapture, settle, fatalOf, unsignedJwt } = require('./helpers');

const USER = 'e2e-proxy-user';

test.skip(process.env.E2E_AUTH_MODE !== 'proxy', 'set E2E_AUTH_MODE=proxy (see file header)');

test.use({
  extraHTTPHeaders: { 'X-Phenix-Auth-Token': 'Bearer ' + unsignedJwt(USER) },
});

async function gotoProxySignup(page) {
  await page.goto('/');
  await settle(page, 3000);
  // router GET login -> 404 (unknown user) -> redirect to the signup form,
  // username carried as a path param
  expect(new URL(page.url()).pathname).toBe('/proxysignup/' + USER);
  await expect(page.locator('.signup-form input[disabled]')).toHaveValue(USER);
  const enabledInputs = page.locator('.signup-form input:not([disabled])');
  await enabledInputs.nth(0).fill('Proxy');
  await enabledInputs.nth(1).fill('Tester');
}

test('unknown proxy user lands on populated signup form; submit is handled cleanly', async ({
  page,
}) => {
  const issues = [];
  attachCapture(page, issues);

  await gotoProxySignup(page);
  await page.getByRole('button', { name: 'Submit' }).click();
  await settle(page, 3000);

  // Either the signup succeeds (user logged in) or the server rejects it with
  // a real error message (e.g. password-policy validation, a known upstream
  // limitation for proxy signups). Both are fine here — what must never
  // happen is a client-side TypeError like the old "e.json is not a function".
  expect(issues.filter((i) => /is not a function/i.test(i.text))).toHaveLength(0);
  const notif = await page.locator('.notification').allTextContents();
  expect(notif.join(' | ')).not.toMatch(/is not a function/i);
});

test('signup success (200) logs the user in via response.data', async ({ page }) => {
  const issues = [];
  attachCapture(page, issues);

  // Regression test for the axios-migration signup bug: on a 200 the client
  // must read response.data (not the fetch API's response.json()) and log the
  // user in. Fulfill the POST with a LoginResponse-shaped body so the success
  // path runs regardless of server-side password policy.
  await page.route('**/api/v1/signup', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      json: {
        user: {
          username: USER,
          first_name: 'Proxy',
          last_name: 'Tester',
          resource_names: ['*', '*/*'],
          role: {
            name: 'Global Admin',
            policies: [{ resources: ['*', '*/*'], resourceNames: ['*', '*/*'], verbs: ['*'] }],
          },
        },
        token: unsignedJwt(USER),
      },
    })
  );

  await gotoProxySignup(page);
  await page.getByRole('button', { name: 'Submit' }).click();

  await expect(page).toHaveURL(/\/experiments/, { timeout: 15000 });
  expect(issues.filter((i) => /is not a function/i.test(i.text))).toHaveLength(0);

  // 401/user-error fallout is expected: the user was never really created
  const fatal = fatalOf(issues).filter((i) => !/401|user error/.test(i.text));
  expect(fatal, JSON.stringify(fatal, null, 2)).toHaveLength(0);
});
