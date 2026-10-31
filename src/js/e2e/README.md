# phenix UI smoke tests

Browser-level smoke tests ([Playwright](https://playwright.dev)) for the
phenix web UI. They drive a real `phenix ui` server — any deployment works:
a CI-built binary, a container, or a full range node.

## What runs where

| Spec                           | Needs                                                                                        | CI                  |
| ------------------------------ | -------------------------------------------------------------------------------------------- | ------------------- |
| `routes.spec.js`               | just a running server (empty store is fine)                                                  | yes                 |
| `forms.spec.js`                | just a running server                                                                        | yes                 |
| `builder-*.spec.js`            | just a running server, except the two specs below                                            | yes                 |
| `builder-files.spec.js`        | also `E2E_BASE_DIR`: the server's `--base-dir.phenix`, on the machine the tests run on       | yes                 |
| `builder-multi-user.spec.js`   | server started with `--jwt-signing-key` and an admin user, and `E2E_MULTI_USER=1`            | yes (own server)    |
| `builder-legacy.spec.js`       | just a running server and `tests/fixtures/legacy-sample.xml`                                 | yes                 |
| `experiment-lifecycle.spec.js` | minimega, VM images, a topology (`E2E_LIFECYCLE=1`)                                          | no (needs minimega) |
| `auth-enabled.spec.js`         | UI built with `VITE_AUTH=enabled`, server `--jwt-signing-key`, `E2E_AUTH_MODE=enabled`       | yes (own job)       |
| `auth-proxy.spec.js`           | UI built with `VITE_AUTH=proxy`, server `--jwt-signing-key proxy-jwt`, `E2E_AUTH_MODE=proxy` | yes (own job)       |

CI (`.github/workflows/frontend.yml`) builds `bin/phenix` three times, with
the UI built with `VITE_AUTH=disabled`, `enabled` and `proxy`. Then it runs
seven jobs in parallel. Each starts its own `bin/phenix ui` against a
throw-away store and base directory (`--base-dir.phenix`, given to the tests
as `E2E_BASE_DIR`) and runs one part of the suite:

| Job                | UI build   | Server                        | Runs                                        |
| ------------------ | ---------- | ----------------------------- | ------------------------------------------- |
| Smoke 1/3 to 3/3   | `disabled` | no flags                      | the default set without `@axe`, in 3 shards |
| Axe scans          | `disabled` | no flags                      | the `@axe` tests, in Chromium only          |
| Builder multi-user | `disabled` | authentication on             | `builder-multi-user.spec.js`, in Chromium   |
| Auth enabled       | `enabled`  | authentication on             | `auth-enabled.spec.js`                      |
| Auth proxy         | `proxy`    | `--jwt-signing-key proxy-jwt` | `auth-proxy.spec.js`, in Chromium           |

The builds run, and the seven jobs run their tests, only for a change that
can affect the browser: `src/js/` outside its unit tests in `src/js/test/`,
`src/go/web/`, `src/go/api/`, `src/go/store/`, `src/go/cmd/`,
`src/go/types/builder/`, `src/go/types/version/`, the `Makefile`, `docker/`
or the workflow itself (`browser_path()` in the workflow).
For any other change the builds and the seven jobs report success without
running a test. On CI, a `test.only` left in a spec fails the run
(`forbidOnly`). A test that fails on CI runs once more. A test that passes
only on that retry is flaky, and it fails the run too (`failOnFlakyTests`).
The trace and screenshot of the failed try are in the job's failure
artifacts. On CI the Playwright run stops after 8 minutes (`globalTimeout`),
and each job stops after 40 minutes.

The seven jobs run in the Playwright image
(`mcr.microsoft.com/playwright:v<version>-noble`, pinned by digest), which
has Chromium, Firefox and their system packages. Its version must be the
`@playwright/test` version in `package-lock.json`, and the workflow's
`changes` job fails while they differ. To update Playwright, change
`package.json` and the lockfile, then the image tag and digest in the
workflow. To find the digest, run
`docker buildx imagetools inspect mcr.microsoft.com/playwright:v<version>-noble`.

The smoke shards and the Auth enabled job run both projects (below) on
their part. The axe, multi-user and Auth proxy jobs run only the `chromium`
project. The multi-user spec creates its own users with the built-in Builder
role and signs them in (`userMaker` in `tests/builder-support.js`). The shards
use Playwright's `--shard`, which splits the tests by count, not by time.

Every route in `routes.spec.js` is also scanned with axe-core (WCAG 2.x A/AA
rule tags) as rendered against the empty store and fails on any violation.
Content that only appears with data (table rows, modals, notifications) is
not covered; the full axe report is attached to each test result.

Tests run in parallel (`fullyParallel`), in 4 workers on CI and half the CPU
cores locally; set `E2E_WORKERS` to a count or a percentage such as `50%` to
override. Every test must therefore create its own users, drafts and configs
with unique names and must not assume anything about the rest of the server's
state. The drafts page lists the drafts of other tests too. A test selects and
deletes only its own drafts, and a test that presses **Select all** never
presses a Delete button. Two projects split the work:

| Project    | Runs                                                           |
| ---------- | -------------------------------------------------------------- |
| `chromium` | every test                                                     |
| `firefox`  | only tests tagged `@cross-browser`, a browser-sensitive subset |

Tag a test in its declaration: `test('…', { tag: '@cross-browser' }, …)`.
`tag` also takes an array of tags. The Firefox subset is for browser
compatibility only. Give it the tests whose behavior depends on the browser
engine: pointer drag, drag-and-drop, focus order, file transfer, clipboard,
IndexedDB, and CSS layout. The `@axe` tag marks the full axe scans: every view
and dialog, a canvas of filled nodes, custom icons and the template editor,
each in every theme. Use it on no other test, except the keyboard and focus
tests in the same `axeScans()` block of `builder-a11y.spec.js`. A
`@cross-browser` test scans only through `expectAccessible()` or
`expectTextContrast()`, which do nothing in Firefox.

### Builder specs

The Builder specs import `test` and `expect` from `tests/builder-support.js`.
Its `builder` fixture wraps the editor in a page object. Its `tracker` fixture
deletes every draft and config that a test creates. One
`builder-integration.spec.js` test routes a non-local host name to the server
to check that the Builder works over plain HTTP from any host.

Test keys, focus and selection in one component with Vitest
(`src/js/test/builder/`, see `src/js/AGENTS.md`). A spec tests only what needs
a browser and a server: the API, CSS and layout, the canvas, and journeys
across views.

A test checks one state, or one journey of linked steps, such as a dialog that
the keyboard opens, uses and closes. It stays under about 300 lines. It starts
from the state it checks, not from the clicks of another test. It seeds that
state through the API: a draft (`seedDraft()` with `blankDocument()` or
`labDocument()`, which takes the hostnames of its devices), a published
topology (`publishTopology()`), a config (`seedConfig()`) or library templates
(`seedTemplates()`). Put a helper that more than one spec needs, such as
`download()` or `watchDraftCreates()`, in `tests/builder-support.js`.

To test what a draft keeps across a page load, use
`builder.editAndReload(draft, options)`. It makes the edit (`options.edit`),
waits until the draft is saved or kept offline (`expectSaved`), and reloads.
Then it opens the draft again from the landing and checks what it shows
(`expectAfterReload`).

The editor's live region holds each message for 750 ms. Then it reads the
messages that arrived in that time together, each as a sentence: "Added
device. Moved alpha." Thus a message can be part of a longer one, so a
`RegExp` for it uses `\b` at its ends, not `^` or `$`. A test that pauses the
page clock must run the clock past the hold to show a waiting message.

Specs do not read the live region text, because the next message can replace
it before an assertion reads it. Every page logs each message that its live
region shows. `expect(builder).toHaveAnnounced('Added device')` waits for a
message in that log. It also takes a `RegExp`, or `{ exact: true }` to match a
whole message. Each call searches after the text that the previous call found,
so a message expected twice must show twice. `.not.toHaveAnnounced()` checks
the log immediately, and `builder.announced()` returns the log.

A check that something does not happen (no tooltip, no message) does not
wait a fixed time first: on a busy machine the page's timers can run late,
and the check then passes before the page could do it. The test installs the
page clock before the page opens (`page.clock.install()`), and runs it past
the delay the UI waits for (`page.clock.runFor(ms)`), which runs every timer
due in that time. Where it can, the test also shows that the same run does
make the positive case happen, such as the tooltip appearing.

A topology can name a Builder file on the server
(`metadata.annotations.builder-doc.path`). phenix reads such a file only below
its base directory. `builder-files.spec.js` writes these files, so it needs the
server on the same machine. Set `E2E_BASE_DIR` to the server's
`--base-dir.phenix`, spelled the same way, because the server messages contain
it. Each test writes to its own directory below
`$E2E_BASE_DIR/e2e-builder-files/` and deletes it after. Without
`E2E_BASE_DIR` the spec skips, so the default set still runs against a remote
server.

All tests share one icon library (`/api/v1/builder/icons`). A test that uploads
an icon makes the image in code with random colors, so no other test has the
same bytes. It deletes the icon after the test, as the `libraryIcons` fixture
of `builder-presentation.spec.js` does. A test that only needs a library to
list answers the route itself (`page.route`). `pngOf`, `iconOf` and `ownColor`
in `tests/builder-support.js` make such an image and the document icon for it.

All tests also share one template library (`/api/v1/builder/templates`). The
Node Templates tab of the drafts page and the Templates list of every diagram
show it. A test can count only on its own templates and the five built-in
ones, which are read-only and not in the library. The templates of other tests
come and go. Each collection is its own section of Templates. A test that
counts those sections loads the library without collections
(`withoutCollections()` in `builder-templates.spec.js`). A test names its
templates and collections with `uniqueName()`, and changes and deletes only
those. It uses Select all and then Delete selected only on the list of a
collection that it made. The `tracker` fixture deletes the templates and
collections that a page or `seedTemplates()` adds. In
`tests/builder-support.js`, `templateLibrary(page)` finds the controls of the
tab, and `changeTemplate()` saves a newer version of a template, as another
user would.

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

# Builder suite, one project at a time
npx playwright test builder --project=chromium
npx playwright test builder --project=firefox

# as the separate CI jobs run them: the axe scans, and one shard of the rest
npx playwright test --grep @axe
npx playwright test --grep-invert @axe --shard=1/3

# Builder files a topology names (the server started with
# `--base-dir.phenix /tmp/phenix-e2e`, a directory this user can write)
E2E_BASE_DIR=/tmp/phenix-e2e npx playwright test builder-files

# several Builder users (the server started with
# `--jwt-signing-key e2e-multi-user --users 'e2e-admin:Testpass1!:Global Admin'`)
E2E_MULTI_USER=1 npx playwright test builder-multi-user

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
