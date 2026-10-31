// Shared helpers for the phenix UI smoke tests.
const { test, expect } = require('@playwright/test');
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

// The first page load of a fresh session redirects to home (store.login()
// runs before store.next is set — long-standing behavior, predates Vue 3).
// Seed the session at '/', then navigate to the real target.
async function gotoSeeded(page, path) {
  await page.goto('/');
  await settle(page, 800);
  await page.goto(path);
}

// Unsigned JWT good enough for proxy mode (the server intentionally parses
// proxy-supplied tokens without verifying the signature).
function unsignedJwt(username) {
  const b64 = (o) => Buffer.from(JSON.stringify(o)).toString('base64url');
  return `${b64({ alg: 'HS256', typ: 'JWT' })}.${b64({ sub: username, exp: 9999999999 })}.sig`;
}

// Axe scans run only in Chromium.
function isChromium(page) {
  return page.context().browser()?.browserType().name() === 'chromium';
}

// Fails when text in `selector` has less contrast than WCAG 1.4.3 asks for,
// as axe-core measures it. `label` names the element. Chromium only.
async function expectTextContrast(page, selector, label) {
  if (!isChromium(page)) return;
  const { violations } = await new AxeBuilder({ page })
    .include(selector)
    .withRules(['color-contrast'])
    .analyze();
  const failures = violations.flatMap((violation) =>
    violation.nodes.map(
      (node) =>
        `${node.target.join(' ')}: ${node.any.map((check) => check.message).join('; ')}`,
    ),
  );
  expect(failures, `text contrast in ${label}`).toEqual([]);
}

// axe-core rule tags that every scan runs. axe-core has no wcag21aaa or
// wcag22aaa tag: its three AAA rules are all tagged wcag2aaa.
const AXE_TAGS = [
  'wcag2a',
  'wcag2aa',
  'wcag2aaa',
  'wcag21a',
  'wcag21aa',
  'wcag22aa',
];

// Scans the page with AXE_TAGS, attaches the full report to the test result
// as `axe-<label>`, and fails on any violation. Run it in Chromium only.
// The scan runs with reduced motion, so a toast that another worker causes
// shows at full opacity at once, and a fade cannot fail color-contrast.
async function expectNoAxeViolations(page, label) {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  let results;
  try {
    await animationsDone(page);
    results = await new AxeBuilder({ page }).withTags(AXE_TAGS).analyze();
  } finally {
    await page.emulateMedia({ reducedMotion: null });
  }
  await test.info().attach(`axe-${label}`, {
    body: JSON.stringify(
      { violations: results.violations, incomplete: results.incomplete },
      null,
      2,
    ),
    contentType: 'application/json',
  });
  expect(
    results.violations.map((v) => `${v.id} (${v.nodes.length})`),
    `axe violations on ${label}; see the axe attachment for details`,
  ).toEqual([]);
}

// Resolves when every animation and transition that ends has ended, so a
// scan does not see a modal in the middle of its fade. Spinners never end.
async function animationsDone(page) {
  await page.evaluate(() =>
    Promise.all(
      document
        .getAnimations()
        .filter((a) => a.effect?.getComputedTiming().endTime !== Infinity)
        .map((a) => a.finished.catch(() => {})),
    ),
  );
}

// Elements (and rendered ::before/::after) that still move for longer than
// 0.01s while the page emulates prefers-reduced-motion: reduce (WCAG 2.3.3).
async function motionOffenders(page) {
  return page.evaluate(() => {
    const seconds = (value) =>
      Math.max(
        ...value.split(',').map((part) => {
          const n = parseFloat(part) || 0;
          return part.trim().endsWith('ms') ? n / 1000 : n;
        }),
      );
    const counts = {};
    for (const el of document.querySelectorAll('*')) {
      for (const pseudo of [null, '::before', '::after']) {
        const s = getComputedStyle(el, pseudo);
        if (pseudo && s.content === 'none') continue;
        const t =
          s.transitionProperty !== 'none' ? seconds(s.transitionDuration) : 0;
        const a = s.animationName !== 'none' ? seconds(s.animationDuration) : 0;
        if (t > 0.01 || a > 0.01) {
          const classes = [...el.classList].slice(0, 3).join('.');
          const key =
            `${el.tagName.toLowerCase()}${classes ? '.' + classes : ''}` +
            `${pseudo || ''} transition=${s.transitionDuration}` +
            ` animation=${a > 0 ? s.animationName + ' ' + s.animationDuration : 'none'}`;
          counts[key] = (counts[key] || 0) + 1;
        }
      }
    }
    return Object.entries(counts).map(([key, n]) => `${key} (${n})`);
  });
}

module.exports = {
  attachCapture,
  settle,
  fatalOf,
  gotoSeeded,
  isChromium,
  unsignedJwt,
  expectTextContrast,
  AXE_TAGS,
  expectNoAxeViolations,
  animationsDone,
  motionOffenders,
};
