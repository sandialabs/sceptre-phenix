# Frontend Instructions

Read root [`AGENTS.md`](../../AGENTS.md) first. This UI uses Vue 3. These rules
apply to Vue, JavaScript/TypeScript, Vite, Vitest, and Playwright work under
`src/js/`.

## Architecture

- `src/views/`: route-level pages; `src/components/`: small reusable UI.
- `src/api/`: one module per REST resource (`vms.js`, `disks.js`, ...) whose
  functions send each request and return the axios promise; pages and
  components call these rather than axios with an inline URL.
- `src/utils/`: shared helpers; `router.js`: routes and guards; `store.js`:
  Pinia state; `main.js`: application setup.
- Vite proxies `/api/v1`, `/version`, `/features`, `/healthz`, `/readyz`, and
  `/webshark` to `localhost:3000`.
- `dist/` is generated output and is copied into `src/go/web/public/`; do not
  treat it as source.

## Setup and Commands

Use Node.js 24 and locked installs:

```bash
nvm use
npm ci
```

| Purpose                  | Command                                                       |
| ------------------------ | ------------------------------------------------------------- |
| Development server       | `npm run dev`                                                 |
| Focused Vitest           | `npm test -- test/rbac.test.js`                               |
| All Vitest               | `npm test`                                                    |
| Vitest with coverage     | `npm run test:coverage`                                       |
| Production build         | `npm run build`                                               |
| Format, then review diff | `npm run format`                                              |
| Unused files and exports | `npm run knip` (also a prek hook; exceptions in `knip.jsonc`) |
| Bundle size budgets      | `cd perf && npm ci && npm run size` (after a build)           |
| Bundle sizes as JSON     | `cd perf && npm run --silent size:json`                       |
| Lighthouse audits        | `cd perf && npm run lighthouse` (needs a running server)      |
| CI perf summary          | `cd perf && node summary.mjs ...` (see `perf/README.md`)      |
| Docs screenshots         | `cd e2e && npm run docs-screenshots` (see its README)         |

The development server needs a backend on `localhost:3000`.

## UI Conventions

- Follow two-space indentation and Prettier.
- Use existing Pinia, router, Axios, Buefy, and component patterns.
- Import Font Awesome icons individually in `src/main.js`; never import the
  entire icon set.
- Page data caches, reuse windows, stale thresholds and the live updates that
  invalidate a cached page are declared in `src/utils/freshness.js`; do not add
  ad hoc caches. Order list data against websocket updates by the server's
  `seq` (see `src/utils/liveRows.js`), never by the browser's clock.
- UI changes must meet WCAG 2.2 AA accessibility guidelines; the Playwright
  routes smoke test enforces this with axe-core (see [e2e/README.md](e2e/README.md)).
- Authentication is selected at build time with
  `VITE_AUTH=enabled|disabled|proxy`. Validate the affected mode and keep UI,
  REST, websocket, and RBAC behavior aligned.
- phēnix is used on desktop browsers only, not on phones or other mobile
  devices. Do not design, test, or take screenshots for phone widths; lay pages
  out for desktop screens.

## Browser Tests

Playwright requires a running `phenix ui`; default URL is
`http://127.0.0.1:3197`, override with `E2E_BASE_URL` (see the port rule in
the root `AGENTS.md`):

```bash
cd e2e
npm ci
npx playwright install --with-deps chromium firefox
# against `phenix ui --listen-endpoint 127.0.0.1:3197`
npx playwright test
```

Specs that only read the store are listed in `playwright.config.js` and run in
parallel; add a new spec there only if it creates, changes and deletes nothing.
Tests time out after 30 s unless their file raises it.

Default smoke tests need only a server. `routes.spec.js` also runs an
axe-core WCAG 2.x A/AA scan on every route; fix violations in the UI
(accessible names, contrast, ARIA) rather than excluding rules. Lifecycle tests additionally need
minimega, VM images, and a topology (`E2E_LIFECYCLE=1`). Auth suites require a
matching `VITE_AUTH` build and signing key. See `e2e/README.md`; report missing
prerequisites instead of silently skipping checks.

Axe scans run only in Chromium. Do not run them in Firefox. The Firefox
projects (`@cross-browser` tests) are only for browser-compatibility tests.

On CI a failed test runs once more, and a test that passes only on that retry
is flaky and fails the run (`failOnFlakyTests`). Fix the cause instead of
adding waits. A check that something does not happen runs the page clock
past the delay the UI waits for (`page.clock.runFor`), not a fixed
`waitForTimeout` (see `e2e/README.md`).

## Unit Tests

Vitest runs in happy-dom (`vite.config.js`); a test needing Node file URLs or
no DOM starts `// @vitest-environment node`. `test/setup.js` gives each test
an empty `localStorage`, clears the page body and unmounts every wrapper.
`test/unit-setup.js` mocks `@/utils/axios.js` and `@/store.js` for every
test file. A file mocks the module again for other values, or calls
`vi.unmock` for the real module. happy-dom has no layout, so test sizes and
positions in Playwright. Shared helpers live in `test/helpers/`:

- Mount with `mount.js`: `mountComponent` (props, data, route, stubs) and
  `mountPage` (also answers the requests the page made as it opened), both
  with the real Buefy and the app's named routes. Its readers (`textOf`,
  `byLabel`, `buttons`, `tableRows`, `tooltipButtons`, `dialogs`,
  `confirmDialog`, `toasts`) read what the page draws.
- Act as a user: click and type with `trigger()` and `setValue()`, then
  `await flushPromises()` for requests and `await nextTick()` for re-renders.
  Assert on the DOM and `emitted()`, not on methods called by hand.
- Page tests load `mocks.js` with `vi.hoisted`, hand its `modules.*`
  factories to `vi.mock`, and call `resetMocks()` in `beforeEach`, passing
  the store the test needs (`resetMocks({ store: {} })`). Add a stand-in for
  each mocked module there rather than in one test file.
- Roles for the mocked `roleAllowed` are `permissions.js` predicates
  (`allowAll`, `denyAll`, `allowOnly`, `denyOnly`); `roles.js` builds role
  objects for the real `rbac.js`. Build disks, VMs, websocket messages and
  failed requests with `fixtures.js`.
- Write repeated permission or state cases as `it.each` tables, each row
  checking both the allowed and the refused case.
- Check escaping, round trips and list invariants as fast-check properties
  beside the named examples, with the shared arbitraries in
  `arbitraries.js`. A property that mounts a component lowers `numRuns` to
  about 20 to keep the file fast.

## Performance

`perf/` holds size-limit budgets, Lighthouse CI assertions, and `summary.mjs`,
which writes the CI budget summary; see `perf/README.md`. `perf/` and `e2e/`
are separate npm projects with their own lockfiles, so a plain `npm ci` here
does not install Lighthouse or Playwright. Register new Buefy components in `src/utils/buefy.js` (or
locally in the one view that uses a heavy one) rather than installing all of
Buefy, keep long-lived third-party objects (xterm, d3, editors) out of deep
reactivity with `markRaw`, and clean up intervals, sockets, and listeners in
`beforeUnmount`.

## CI

`.github/workflows/frontend.yml` runs Vitest with coverage, builds the UI and
a real backend once for each `VITE_AUTH` mode, then runs the Playwright tests
in parallel jobs, each against its own server started with
`scripts/ci-start-phenix-ui.sh` (see `e2e/README.md`). Its `perf` job runs
bundle budgets and Lighthouse CI against the `VITE_AUTH=disabled` build. It
builds and runs the Playwright tests and `perf` only for a change that can
affect the browser, as its `changes` job decides. A change to unit tests under
`test/` alone runs Vitest only. Every job has read-only permissions; `perf`
also reads the default branch's `perf-summary` artifact to report deltas, and
its final Check budgets step is what fails it. Keep Node versions, npm cache
lockfiles, auth build mode, backend startup, path filters, and that job's path
list aligned with local commands. The Playwright jobs run in the Playwright
image pinned in the workflow. When you change the `@playwright/test` version in
`e2e/`, change the image tag and digest to match. The `changes` job fails while
they differ.
`.github/workflows/security.yml` reports `npm audit` findings for the `src/js`,
`e2e/` and `perf/` lockfiles on pull requests and weekly without failing; run
`scripts/vuln-report.sh npm` from the repository root for the same report.
