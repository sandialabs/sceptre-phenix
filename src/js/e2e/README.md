# phenix UI smoke tests

Browser-level smoke tests ([Playwright](https://playwright.dev)) for the
phenix web UI. They drive a real `phenix ui` server — any deployment works:
a CI-built binary, a container, or a full range node.

## What runs where

| Spec                           | Needs                                                                        | CI                               |
| ------------------------------ | ---------------------------------------------------------------------------- | -------------------------------- |
| `routes.spec.js`               | just a running server (empty store is fine)                                  | yes                              |
| `forms.spec.js`                | just a running server                                                        | yes                              |
| `builder*.spec.js`             | server started with `--features builder-beta`                                | yes                              |
| `builder-feature-off.spec.js`  | server started without `--features builder-beta`, and `E2E_BUILDER_BETA=off` | yes (second server)              |
| `experiment-lifecycle.spec.js` | minimega, VM images, a topology                                              | opt-in (`E2E_LIFECYCLE=1`)       |
| `auth-enabled.spec.js`         | UI built with `VITE_AUTH=enabled`, server `--jwt-signing-key`                | opt-in (`E2E_AUTH_MODE=enabled`) |
| `auth-proxy.spec.js`           | UI built with `VITE_AUTH=proxy`, server `--jwt-signing-key proxy-jwt`        | opt-in (`E2E_AUTH_MODE=proxy`)   |

CI (`.github/workflows/frontend.yml`) builds the UI with `VITE_AUTH=disabled`,
starts `bin/phenix ui --features builder-beta` against a throw-away store, and
runs the default set. It then starts a second server without the flag and
runs `builder-feature-off.spec.js` against it. Builder checks include axe
accessibility scans.

Tests run in parallel (`fullyParallel`), in 4 workers on CI and half the CPU
cores locally; set `E2E_WORKERS` to override. Every test must therefore create
its own drafts and configs with unique names and must not assume anything
about the rest of the server's state. Three projects split the work:

| Project         | Runs                                                           |
| --------------- | -------------------------------------------------------------- |
| `chromium`      | every test except `@known-defect` ones                         |
| `firefox`       | only tests tagged `@cross-browser`, a browser-sensitive subset |
| `known-defects` | only `@known-defect` tests, in Chromium, with a 3s expect wait |

Tag a test in its declaration: `test('…', { tag: '@cross-browser' }, …)`.
Give the Firefox subset the tests whose behavior depends on the browser
engine: pointer drag, drag-and-drop, focus order, file transfer, clipboard,
IndexedDB, and CSS layout.

### Builder Flow specs

The Builder specs import `test` and `expect` from `tests/builder-support.js`.
Its `builder` fixture wraps the editor in a page object, and its `tracker`
fixture deletes every draft and config a test creates, so runs leave the
server clean. Builder Flow works over plain HTTP from any host, which one
`builder-integration.spec.js` test checks by routing a non-local host name to
the server.

A test that asserts the intended behavior of a known product defect is tagged
`@known-defect` and starts with `knownDefect('<summary> (<finding id>)')`
(`knownDefect()` throws when the tag is missing). Playwright then expects that
test to fail and reports it as passing. Once the defect is fixed, the test
reports "unexpectedly passed"; remove the marker and the tag in the same
change as the fix. List the open defects with:

```bash
grep -rn "knownDefect('" tests/
```

## Running locally

```bash
cd src/js/e2e
npm ci
npx playwright install --with-deps chromium firefox

# The default target is a server on :3000. Locally, start phenix on a free
# port instead (the root AGENTS.md lists ports to avoid) and point the tests
# at it, for example `phenix ui --listen-endpoint 127.0.0.1:3080
# --features builder-beta`:
export E2E_BASE_URL=http://127.0.0.1:3080

# default set
npx playwright test

# Builder suite, one project at a time
npx playwright test builder --project=chromium
npx playwright test builder --project=firefox
npx playwright test builder --project=known-defects

# Builder with the feature flag off (a second server, started without
# --features, for example on 127.0.0.1:3081)
E2E_BUILDER_BETA=off E2E_BASE_URL=http://127.0.0.1:3081 \
  npx playwright test builder-feature-off

# full experiment lifecycle on a real deployment (topology defaults to helloworld)
E2E_LIFECYCLE=1 E2E_TOPOLOGY=helloworld npx playwright test experiment-lifecycle
```

The auth-mode specs need the UI rebuilt with the matching `VITE_AUTH` value and
the server restarted with the matching `--jwt-signing-key`; see the header
comment in each spec. The proxy spec simulates the auth proxy with Playwright's
`extraHTTPHeaders` — no external proxy required.

Failure artifacts (screenshots, traces) land in `test-results/`; open traces
with `npx playwright show-trace <trace.zip>`.
