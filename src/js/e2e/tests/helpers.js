// Shared helpers for the phenix UI smoke tests.

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
  page.on('pageerror', (err) => issues.push({ kind: 'pageerror', text: String(err) }));
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
      issues.push({ kind: 'requestfailed', text: req.method() + ' ' + req.url() + ' -> ' + f.errorText });
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
    if (/^Failed to load resource/.test(i.text) && (i.loc || '').includes('/api/')) return false;
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

module.exports = { attachCapture, settle, fatalOf, gotoSeeded, unsignedJwt };
