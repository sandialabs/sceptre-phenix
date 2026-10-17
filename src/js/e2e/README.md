# phenix UI smoke tests

Browser-level smoke tests ([Playwright](https://playwright.dev)) for the
phenix web UI. They drive a real `phenix ui` server — any deployment works:
a CI-built binary, a container, or a full range node.

## What runs where

| Spec                           | Needs                                                                                           | CI                  |
| ------------------------------ | ----------------------------------------------------------------------------------------------- | ------------------- |
| `routes.spec.js`               | just a running server (empty store is fine)                                                     | yes                 |
| `forms.spec.js`                | just a running server                                                                           | yes                 |
| `builder-*.spec.js`            | just a running server (the Builder)                                                             | yes                 |
| `builder-presentation.spec.js` | as above: a device's type, info tooltips, templates, colors, line styles, group fields, icons   | yes                 |
| `builder-files.spec.js`        | as above, and `E2E_BASE_DIR`: the server's `--base-dir.phenix`, on the machine the tests run on | yes                 |
| `builder-legacy.spec.js`       | just a running server; converts `tests/fixtures/legacy-sample.xml`, a legacy Builder diagram    | yes                 |
| `builder-drafts.spec.js`       | just a running server; the drafts page: its cards, Publish from a card, Exp, and bulk Delete    | yes                 |
| `builder-templates.spec.js`    | just a running server; device templates of a diagram and of the library, and its collections    | yes                 |
| `builder-layers.spec.js`       | just a running server; the Layers tab, the layers editor, node matchers and layer outlines      | yes                 |
| `builder-multi-user.spec.js`   | server started with `--jwt-signing-key` and an admin user, and `E2E_MULTI_USER=1`               | yes (own server)    |
| `experiment-lifecycle.spec.js` | minimega, VM images, a topology (`E2E_LIFECYCLE=1`)                                             | no (needs minimega) |
| `auth-enabled.spec.js`         | UI built with `VITE_AUTH=enabled`, server `--jwt-signing-key`, `E2E_AUTH_MODE=enabled`          | yes (own job)       |
| `auth-proxy.spec.js`           | UI built with `VITE_AUTH=proxy`, server `--jwt-signing-key proxy-jwt`, `E2E_AUTH_MODE=proxy`    | yes (own job)       |

CI (`.github/workflows/frontend.yml`) builds `bin/phenix` three times, with
the UI built with `VITE_AUTH=disabled`, `enabled` and `proxy`. Then it runs
seven jobs in parallel. Each starts its own `bin/phenix ui` against a
throw-away store and base directory (`--base-dir.phenix`, which
`E2E_BASE_DIR` names to the tests) and runs one part of the suite:

| Job                | UI build   | Server                        | Runs                                        |
| ------------------ | ---------- | ----------------------------- | ------------------------------------------- |
| Smoke 1/3 to 3/3   | `disabled` | no flags                      | the default set without `@axe`, in 3 shards |
| Axe scans          | `disabled` | no flags                      | the `@axe` tests, in Chromium only          |
| Builder multi-user | `disabled` | authentication on             | `builder-multi-user.spec.js`, in Chromium   |
| Auth enabled       | `enabled`  | authentication on             | `auth-enabled.spec.js`                      |
| Auth proxy         | `proxy`    | `--jwt-signing-key proxy-jwt` | `auth-proxy.spec.js`, in Chromium           |

The builds run, and the seven jobs run their tests, only for a change that
can affect the browser: `src/js/` outside its unit tests in `src/js/test/`,
`src/go/web/`, `src/go/api/builder/`, `src/go/types/builder/`,
`src/go/types/version/`, the `Makefile`, `docker/` or the workflow itself.
For any other change the builds and the seven jobs report success without
running a test. On CI, a `test.only` left in a spec fails the run
(`forbidOnly`).

The smoke shards and the Auth enabled job run all three projects (below) on
their part. The axe, multi-user and Auth proxy jobs run only the `chromium`
project. The multi-user spec makes and signs in users of its own, of the
built-in Builder role (`userMaker` in `tests/builder-support.js`). Axe scans run
only in Chromium: `expectAccessible()` and `expectTextContrast()` do nothing in
Firefox. The shards use Playwright's `--shard`, which splits the tests by count,
not by time.

Every route in `routes.spec.js` is also scanned with axe-core (WCAG 2.x A/AA
rule tags) as rendered against the empty store and fails on any violation.
Content that only appears with data (table rows, modals, notifications) is
not covered; the full axe report is attached to each test result.

Tests run in parallel (`fullyParallel`), in 4 workers on CI and half the CPU
cores locally; set `E2E_WORKERS` to override. Every test must therefore create
its own drafts and configs with unique names and must not assume anything
about the rest of the server's state. The drafts page lists the other tests'
drafts too, so a test selects and deletes only what it made, and a test that
presses **Select all** never presses a Delete button. Three projects split the
work:

| Project         | Runs                                                           |
| --------------- | -------------------------------------------------------------- |
| `chromium`      | every test except `@known-defect` ones                         |
| `firefox`       | only tests tagged `@cross-browser`, a browser-sensitive subset |
| `known-defects` | only `@known-defect` tests, in Chromium, with a 3s expect wait |

Tag a test in its declaration: `test('…', { tag: '@cross-browser' }, …)`.
`tag` also takes an array of tags. The Firefox subset is for browser
compatibility only. Give it the tests whose behavior depends on the browser
engine: pointer drag, drag-and-drop, focus order, file transfer, clipboard,
IndexedDB, and CSS layout. The `@axe` tag marks the axe scans of every view and
dialog in each theme, and the scans of a canvas of filled nodes, of custom icons
and of the template editor in each theme, which CI runs in a job of their own,
in Chromium only. Keep it off other tests, except the keyboard and focus tests
that sit in the same `axeScans()` block of `builder-a11y.spec.js`. A
`@cross-browser` test scans only through `expectAccessible()` or
`expectTextContrast()`, which skip Firefox.

### Builder specs

The Builder specs import `test` and `expect` from `tests/builder-support.js`.
Its `builder` fixture wraps the editor in a page object, and its `tracker`
fixture deletes every draft and config a test creates, so runs leave the
server clean. Builder works over plain HTTP from any host, which one
`builder-integration.spec.js` test checks by routing a non-local host name to
the server.

A test checks one state, or one journey whose steps lead from one to the
next, such as a dialog opened, used and closed by keyboard, and stays under
about 300 lines. It starts from the state it checks: it seeds a draft through
the API (`seedDraft()` with `blankDocument()` or `labDocument()`, which takes
the hostnames of its devices), a published topology (`publishTopology()`), a
config (`seedConfig()`) or library templates (`seedTemplates()`), rather than
clicking through the steps of another test. A helper that more than one spec
needs, such as `download()` or `watchDraftCreates()`, goes in
`tests/builder-support.js`. A test of what a draft keeps
across a new page load goes through `builder.editAndReload(draft, options)`:
the edit (`options.edit`), the wait until it is saved or kept offline
(`expectSaved`), the reload, the draft opened again from the landing, and
what it shows then (`expectAfterReload`).

The editor's live region holds each message for 750 ms, and messages that
arrive meanwhile are read together after it. A spec that does not test what
screen readers hear, or how messages are paced, may shorten the hold with
`test.use({ announceHold: 100 })`, as the inspector and editing specs do: the
fixture sets `window.__BUILDER_ANNOUNCE_HOLD_MS__`, which the live region
reads.

The next message can replace one before an assertion on the region reads it,
more so on a slow machine, so specs do not read the live region's text. Every
page logs each message its live region shows, and
`expect(builder).toHaveAnnounced('Added device')` (or a `RegExp`, or
`{ exact: true }` for a whole message) waits for one in that log. Each call
looks after the text the previous one found, so a message expected twice
must be shown twice. `.not.toHaveAnnounced()` checks the log at once, and
`builder.announced()` returns it.

A topology can name a Builder file on the server
(`metadata.annotations.builder-doc.path`), which phenix reads only below its
base directory. `builder-files.spec.js` writes such files itself, so it needs
the server on the same machine and `E2E_BASE_DIR` set to the directory the
server was started with as `--base-dir.phenix`, spelled the same way: the
server's messages name it. Each test writes below
`$E2E_BASE_DIR/e2e-builder-files/`, in a directory of its own that it removes
afterwards. Without
`E2E_BASE_DIR` the spec skips, so the default set still runs against a remote
server.

With authentication off every test is the same user, so all of them share one
icon library (`/api/v1/builder/icons`). A test that uploads an icon builds the
image in code, in colors drawn at random, so no other test has its bytes, and
deletes the icon afterwards: the `libraryIcons` fixture of
`builder-presentation.spec.js` does that. A test that only needs a library to
list answers the route itself (`page.route`). `pngOf`, `iconOf` and `ownColor`
in `tests/builder-support.js` build such an image and the icon a document
carries for it.

They share one template library too (`/api/v1/builder/templates`), which the
server keeps for every user, and which the drafts page's Node Templates tab
and every diagram's Templates list. So a test counts on nothing but its own
templates being there, and the five built-in ones, which are apart from the
library and read only: other tests' come and go meanwhile. Each collection is
a section of Templates of its own, so a test that counts those sections has
its page read the library without collections (`withoutCollections()` in
`builder-templates.spec.js`). It names its templates and collections with
`uniqueName()`, and changes and deletes only those. It never presses Select
all and then Delete selected on the list of every template, only on the list
of a collection it made. The `tracker` fixture deletes the templates and
collections a page adds, and those added with `seedTemplates()` of
`tests/builder-support.js`; `templateLibrary(page)` there finds the tab's
controls, and `changeTemplate()` saves a newer version of a template, as
another user would.

A test that asserts the intended behavior of a known product defect is tagged
`@known-defect` and starts with
`knownDefect('<summary> (<issue or PR, such as sandialabs/sceptre-phenix#436>)')`
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
# at it, for example `phenix ui --listen-endpoint 127.0.0.1:3080`:
export E2E_BASE_URL=http://127.0.0.1:3080

# default set
npx playwright test

# Builder suite, one project at a time
npx playwright test builder --project=chromium
npx playwright test builder --project=firefox
npx playwright test builder --project=known-defects

# the parts CI runs in separate jobs: the full axe scans, and one shard of
# the rest
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
