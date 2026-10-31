# phenix UI smoke tests

Browser-level smoke tests ([Playwright](https://playwright.dev)) for the
phenix web UI. They drive a real `phenix ui` server — any deployment works:
a CI-built binary, a container, or a full range node.

## What runs where

| Spec                           | Needs                                                                                        | CI                  |
| ------------------------------ | -------------------------------------------------------------------------------------------- | ------------------- |
| `routes.spec.js`               | just a running server (empty store is fine)                                                  | yes                 |
| `forms.spec.js`                | just a running server                                                                        | yes                 |
| `a11y-seeded.spec.js`          | just a running server (each test seeds and deletes its own data)                             | yes                 |
| `experiment-lifecycle.spec.js` | minimega, VM images, a topology (`E2E_LIFECYCLE=1`)                                          | no (needs minimega) |
| `auth-enabled.spec.js`         | UI built with `VITE_AUTH=enabled`, server `--jwt-signing-key`, `E2E_AUTH_MODE=enabled`       | yes (own job)       |
| `auth-proxy.spec.js`           | UI built with `VITE_AUTH=proxy`, server `--jwt-signing-key proxy-jwt`, `E2E_AUTH_MODE=proxy` | yes (own job)       |

CI (`.github/workflows/frontend.yml`) builds `bin/phenix` three times, with
the UI built with `VITE_AUTH=disabled`, `enabled` and `proxy`. Then it runs
five jobs in parallel. Each starts its own `bin/phenix ui` against a
throw-away store and runs one part of the suite:

| Job              | UI build   | Server                        | Runs                              |
| ---------------- | ---------- | ----------------------------- | --------------------------------- |
| Smoke 1/3 to 3/3 | `disabled` | no flags                      | the default set, in 3 shards      |
| Auth enabled     | `enabled`  | authentication on             | `auth-enabled.spec.js`            |
| Auth proxy       | `proxy`    | `--jwt-signing-key proxy-jwt` | `auth-proxy.spec.js`, in Chromium |

The builds run, and the five jobs run their tests, only for a change that
can affect the browser: `src/js/` outside its unit tests in `src/js/test/`,
`src/go/web/`, `src/go/api/`, `src/go/store/`, `src/go/cmd/`,
`src/go/types/version/`, the `Makefile`, `docker/` or the workflow itself
(`browser_path()` in the workflow).
For any other change the builds and the five jobs report success without
running a test. On CI, a `test.only` left in a spec fails the run
(`forbidOnly`). A test that fails on CI runs once more. A test that passes
only on that retry is flaky, and it fails the run too (`failOnFlakyTests`).
The trace and screenshot of the failed try are in the job's failure
artifacts.

The five jobs run in the Playwright image
(`mcr.microsoft.com/playwright:v<version>-noble`, pinned by digest), which
has Chromium, Firefox and their system packages. Its version must be the
`@playwright/test` version in `package-lock.json`, and the workflow's
`changes` job fails while they differ. To update Playwright, change
`package.json` and the lockfile, then the image tag and digest in the
workflow. To find the digest, run
`docker buildx imagetools inspect mcr.microsoft.com/playwright:v<version>-noble`.

The smoke shards and the Auth enabled job run both projects (below) on
their part. The Auth proxy job runs only the `chromium` project. The shards
use Playwright's `--shard`, which splits the tests by count, not by time.

Every route in `routes.spec.js` is also scanned with axe-core (WCAG 2.x A, AA
and AAA rule tags, with `wcag2aaa`) as rendered against the empty store and
fails on any violation. `a11y-seeded.spec.js` seeds a topology, a scenario, a
stopped experiment and a user through the REST API, without minimega. It
scans the table rows, modals and config editor that only appear with data,
with the same rules (`expectNoAxeViolations` in `helpers.js`). It also fails
when the config editor traps the keyboard, or when anything moves for longer
than 0.01s while the browser asks for reduced motion. Notifications are not
covered; the full axe report is attached to each test result.

Tests run in parallel (`fullyParallel`), in 4 workers on CI and half the CPU
cores locally; set `E2E_WORKERS` to a count or a percentage such as `50%` to
override. Every test must therefore create
its own users and configs with unique names and must not assume anything
about the rest of the server's state. Two projects split the work:

| Project    | Runs                                                           |
| ---------- | -------------------------------------------------------------- |
| `chromium` | every test                                                     |
| `firefox`  | only tests tagged `@cross-browser`, a browser-sensitive subset |

Tag a test in its declaration: `test('…', { tag: '@cross-browser' }, …)`.
`tag` also takes an array of tags. The Firefox subset is for browser
compatibility only. Give it the tests whose behavior depends on the browser
engine: pointer drag, drag-and-drop, focus order, file transfer, clipboard,
IndexedDB, and CSS layout. Axe scans run only in Chromium, so keep them out
of `@cross-browser` tests.

A check that something does not happen (no tooltip, no message) does not
wait a fixed time first: on a busy machine the page's timers can run late,
and the check then passes before the page could do it. The test installs the
page clock before the page opens (`page.clock.install()`), and runs it past
the delay the UI waits for (`page.clock.runFor(ms)`), which runs every timer
due in that time. Where it can, the test also shows that the same run does
make the positive case happen, such as the tooltip appearing.

## Running locally

```bash
cd src/js/e2e
npm ci
npx playwright install --with-deps chromium firefox

# The default target is a server on :3000. Locally, start phenix on a free
# port instead (the root AGENTS.md lists ports to avoid) and point the tests
# at it, for example `phenix ui --listen-endpoint 127.0.0.1:3080`:
export E2E_BASE_URL=http://127.0.0.1:3080

# default set
npx playwright test

# one shard of the default set, as CI runs it
npx playwright test --shard=1/3

# the auth specs (the UI built with the matching VITE_AUTH, and the server
# started as the header comment of each spec says)
E2E_AUTH_MODE=enabled npx playwright test auth-enabled
E2E_AUTH_MODE=proxy npx playwright test auth-proxy

# full experiment lifecycle on a real deployment (topology defaults to
# helloworld). CI does not run it: it needs minimega and VM images.
E2E_LIFECYCLE=1 E2E_TOPOLOGY=helloworld npx playwright test experiment-lifecycle
```

The auth-mode specs need the UI rebuilt with the matching `VITE_AUTH` value and
the server restarted with the matching `--jwt-signing-key`; see the header
comment in each spec. The proxy spec simulates the auth proxy with Playwright's
`extraHTTPHeaders` — no external proxy required.

Failure artifacts (screenshots, traces) land in `test-results/`; open traces
with `npx playwright show-trace <trace.zip>`.
