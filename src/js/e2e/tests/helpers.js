// Shared helpers for the phenix UI smoke tests.
const base = require('@playwright/test');
const AxeBuilder = require('@axe-core/playwright').default;

// Attach console/network/pageerror capture to a page; findings pushed into `issues`.
function attachCapture(page, issues) {
  page.on('console', (msg) => {
    if (msg.type() === 'error' || msg.type() === 'warning') {
      const loc = msg.location();
      issues.push({
        kind: 'console-' + msg.type(),
        text: msg.text(),
        loc: (loc.url || '') + ':' + (loc.lineNumber || 0),
      });
    }
  });
  page.on('pageerror', (err) =>
    issues.push({ kind: 'pageerror', text: String(err) }),
  );
  page.on('response', (resp) => {
    if (resp.status() >= 400) {
      issues.push({
        kind: 'http-' + resp.status(),
        text: resp.request().method() + ' ' + resp.url(),
      });
    }
  });
  page.on('requestfailed', (req) => {
    const f = req.failure();
    if (f && f.errorText !== 'net::ERR_ABORTED') {
      issues.push({
        kind: 'requestfailed',
        text: req.method() + ' ' + req.url() + ' -> ' + f.errorText,
      });
    }
  });
}

async function settle(page, ms = 2500) {
  await page.waitForLoadState('load').catch(() => {});
  await page.waitForTimeout(ms);
}

// JS errors and page crashes are always fatal. Browser-generated
// "Failed to load resource" console entries for API URLs are not: they are
// emitted even for HTTP errors the app handles deliberately (e.g. the
// running-experiment view probes GET .../netflow and treats 404 as "off").
function fatalOf(issues) {
  return issues.filter((i) => {
    if (i.kind !== 'pageerror' && i.kind !== 'console-error') return false;
    if (
      /^Failed to load resource/.test(i.text) &&
      (i.loc || '').includes('/api/')
    )
      return false;
    return true;
  });
}

// Playwright's `test` with the page's findings captured into `issues`; the
// test fails on any fatal one (see fatalOf) once its body has run.
const test = base.test.extend({
  issues: [
    async ({ page }, use) => {
      const issues = [];
      attachCapture(page, issues);
      await use(issues);

      const fatal = fatalOf(issues);
      base.expect(fatal, JSON.stringify(fatal, null, 2)).toHaveLength(0);
    },
    { auto: true },
  ],
});

// Resolves once the page on screen has loaded its data, which the header's
// refresh status reports.
async function pageDataLoaded(page) {
  await base.expect(page.getByText('Updated just now')).toBeVisible();
}

// Fails the test on any WCAG 2.2 A or AA violation axe-core finds on the page
// as it is now, and attaches the full results as `axe-<name>`.
async function expectAccessible(page, name) {
  const results = await new AxeBuilder({ page })
    .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa'])
    .analyze();
  await base.test.info().attach(`axe-${name}`, {
    body: JSON.stringify(
      { violations: results.violations, incomplete: results.incomplete },
      null,
      2,
    ),
    contentType: 'application/json',
  });
  base
    .expect(
      results.violations.map((v) => `${v.id} (${v.nodes.length})`),
      `axe violations on ${name}; see the axe attachment for details`,
    )
    .toEqual([]);
}

// A Role config that may list experiments, for tests that need a config of
// their own.
const roleConfig = (name) => ({
  apiVersion: 'phenix.sandia.gov/v1',
  kind: 'Role',
  metadata: { name },
  spec: {
    roleName: name,
    policies: [
      { resources: ['experiments'], resourceNames: ['*'], verbs: ['list'] },
    ],
  },
});

// Deletes a config through the REST API; a config that isn't there is fine.
const deleteConfig = (request, config) =>
  request.delete(
    `/api/v1/configs/${config.kind.toLowerCase()}/${config.metadata.name}`,
  );

// Creates a config through the REST API, replacing one an earlier run left.
async function createConfig(request, config) {
  await deleteConfig(request, config);
  const created = await request.post('/api/v1/configs', { data: config });
  base.expect(created.ok(), await created.text()).toBe(true);
}

// Unsigned JWT good enough for proxy mode (the server intentionally parses
// proxy-supplied tokens without verifying the signature).
function unsignedJwt(username) {
  const b64 = (o) => Buffer.from(JSON.stringify(o)).toString('base64url');
  return `${b64({ alg: 'HS256', typ: 'JWT' })}.${b64({ sub: username, exp: 9999999999 })}.sig`;
}

module.exports = {
  test,
  expect: base.expect,
  attachCapture,
  settle,
  fatalOf,
  pageDataLoaded,
  expectAccessible,
  roleConfig,
  createConfig,
  deleteConfig,
  unsignedJwt,
};
