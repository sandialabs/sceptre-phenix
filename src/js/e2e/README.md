# phenix UI smoke tests

Browser-level smoke tests ([Playwright](https://playwright.dev)) for the
phenix web UI. They drive a real `phenix ui` server — any deployment works:
a CI-built binary, a container, or a full range node.

## What runs where

| Spec                           | Needs                                                                                        | CI                      |
| ------------------------------ | -------------------------------------------------------------------------------------------- | ----------------------- |
| `routes.spec.js`               | just a running server (empty store is fine)                                                  | yes                     |
| `forms.spec.js`                | just a running server                                                                        | yes                     |
| `performance.spec.js`          | just a running server                                                                        | yes                     |
| `experiments.spec.js`          | just a running server                                                                        | yes                     |
| `navbar.spec.js`               | just a running server                                                                        | yes                     |
| `tables.spec.js`               | just a running server                                                                        | yes                     |
| `jobs.spec.js`                 | just a running server                                                                        | yes                     |
| `health.spec.js`               | a running server with a BoltDB store and no minimega                                         | yes, in every auth mode |
| `experiment-lifecycle.spec.js` | minimega, VM images, a topology (`E2E_LIFECYCLE=1`)                                          | no (needs minimega)     |
| `auth-enabled.spec.js`         | UI built with `VITE_AUTH=enabled`, server `--jwt-signing-key`, `E2E_AUTH_MODE=enabled`       | yes (own job)           |
| `auth-proxy.spec.js`           | UI built with `VITE_AUTH=proxy`, server `--jwt-signing-key proxy-jwt`, `E2E_AUTH_MODE=proxy` | yes (own job)           |

CI (`.github/workflows/frontend.yml`) builds `bin/phenix` three times, with
the UI built with `VITE_AUTH=disabled`, `enabled` and `proxy`, since
`VITE_AUTH` is compiled into the UI. Each build, with its UI, is the
`phenix-e2e-<mode>` artifact; the `perf` job also uses the `disabled` one.
Then five jobs run in parallel. Each starts its own `bin/phenix ui` against a
throw-away store with `scripts/ci-start-phenix-ui.sh` and runs one part of
the suite:

| Job               | UI build   | Server                        | Runs                                            |
| ----------------- | ---------- | ----------------------------- | ----------------------------------------------- |
| Smoke 1/3         | `disabled` | no flags                      | the `read-only` project                         |
| Smoke 2/3 and 3/3 | `disabled` | no flags                      | the `store` and `firefox` projects, in 2 shards |
| Auth enabled      | `enabled`  | authentication on             | `auth-enabled.spec.js` and `health.spec.js`     |
| Auth proxy        | `proxy`    | `--jwt-signing-key proxy-jwt` | the same for proxy, in Chromium only            |

The builds, the five jobs and `perf` run only for a change that can affect
the browser: `src/js/` outside its unit tests in `src/js/test/`,
`src/go/web/`, `src/go/api/`, `src/go/store/`, `src/go/cmd/`,
`src/go/types/version/`, the `Makefile`, `docker/`, `scripts/api-docs.sh`,
`scripts/ci-start-phenix-ui.sh` or the workflow itself (`browser_path()` in
the workflow). For any other change the builds and the five jobs report
success without running a test, and `perf` is skipped. On CI, a `test.only`
left in a spec fails the run (`forbidOnly`). A test that fails on CI runs
once more. A test that passes only on that retry is flaky, and it fails the
run too (`failOnFlakyTests`). The trace and screenshot of the failed try are
in the job's failure artifacts.

The five jobs run in the Playwright image
(`mcr.microsoft.com/playwright:v<version>-noble`, pinned by digest), which
has Chromium, Firefox and their system packages. Its version must be the
`@playwright/test` version in `package-lock.json`, and the workflow's
`changes` job fails while they differ. To update Playwright, change
`package.json` and the lockfile, then the image tag and digest in the
workflow. To find the digest, run
`docker buildx imagetools inspect mcr.microsoft.com/playwright:v<version>-noble`.

Specs that need data (a topology, experiments, configs) set it up through the
REST API and remove what they made afterwards. Their experiments are never
started, so they need no minimega.

Every route in `routes.spec.js` is also scanned with axe-core (WCAG 2.x A/AA
rule tags) as rendered against the empty store, and the Disks page and its
details window once more with images in folders and outside the files directory.
`experiments.spec.js` scans the pages it fills with data: the experiment list, a
stopped experiment's page with a VM selected, its VM info modal and Files tab,
and the create experiment modal with Advanced Options open. The server has no
disk images, so these two specs stand in for its disk list (`GET /api/v1/disks`)
with `page.route`. Any violation fails the test, and the full axe report is
attached to each test result. Content these scans don't reach, such as
notifications and the running experiment page, is not covered.

## Projects

`playwright.config.js` splits the default set in three projects:

| Project     | Browser  | Runs                                                                                   |
| ----------- | -------- | -------------------------------------------------------------------------------------- |
| `read-only` | Chromium | the specs that never change the store (routes, performance, jobs, health), in parallel |
| `store`     | Chromium | the rest, one at a time once `read-only` finishes                                      |
| `firefox`   | Firefox  | only tests tagged `@cross-browser`, one at a time once `store` finishes                |

The read-only specs therefore always see the empty store. A failure in
`read-only` skips `store`. With `E2E_AUTH_MODE` set, only that mode's spec and
`health.spec.js` run, one at a time, in the `auth-<mode>` project (Chromium)
and the `auth-<mode>-firefox` project (`@cross-browser` tests), since the
other specs assume auth is disabled.

Parallel tests run in 4 workers on CI and half the CPU cores locally; set
`E2E_WORKERS` to a count or a percentage such as `50%` to override. Tests
time out after 30 seconds; the lifecycle spec raises that for its own
file.

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

# default set against a server on :3197 (override with E2E_BASE_URL; the
# root AGENTS.md lists ports to avoid)
npx playwright test

# one part of the default set, as CI runs it
npx playwright test --project=read-only
npx playwright test --project=store --project=firefox --no-deps --shard=1/2

# the auth specs (the UI built with the matching VITE_AUTH, and the server
# started as the header comment of each spec says)
E2E_AUTH_MODE=enabled npx playwright test auth-enabled
E2E_AUTH_MODE=proxy npx playwright test auth-proxy

# full experiment lifecycle on a real deployment (topology defaults to
# helloworld). CI does not run it: it needs minimega and VM images.
# --no-deps skips the read-only project a single store spec otherwise waits for
E2E_LIFECYCLE=1 E2E_TOPOLOGY=helloworld npx playwright test --no-deps experiment-lifecycle
```

The auth-mode specs need the UI rebuilt with the matching `VITE_AUTH` value and
the server restarted with the matching `--jwt-signing-key`; see the header
comment in each spec. The proxy spec simulates the auth proxy with Playwright's
`extraHTTPHeaders` — no external proxy required.

Failure artifacts (screenshots, traces) land in `test-results/`; open traces
with `npx playwright show-trace <trace.zip>`.

## Docs screenshots

`docs-screenshots/` holds an on-demand script, never run in CI, that retakes
the web UI images in `docs/content/images` against a running server with sample
data (`npm run docs-screenshots`); see its
[README](docs-screenshots/README.md).
