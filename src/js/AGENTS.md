# Frontend Instructions

Read root [`AGENTS.md`](../../AGENTS.md) first. This UI uses Vue 3. These rules
apply to Vue, JavaScript/TypeScript, Vite, Vitest, and Playwright work under
`src/js/`.

## Architecture

- `src/views/`: route-level pages; `src/components/`: small reusable UI.
- `src/utils/`: shared helpers; `router.js`: routes and guards; `store.js`:
  Pinia state; `main.js`: application setup.
- Vite proxies `/api/v1`, `/version`, and `/features` to `localhost:3000`.
- `dist/` is generated output and is copied into `src/go/web/public/`; do not
  treat it as source.

## Setup and Commands

Use Node.js 24 and locked installs:

```bash
nvm use
npm ci
```

| Purpose                  | Command                         |
| ------------------------ | ------------------------------- |
| Development server       | `npm run dev`                   |
| Focused Vitest           | `npm test -- test/rbac.test.js` |
| All Vitest               | `npm test`                      |
| Contract tests only      | `npm run test:contract`         |
| Vitest with coverage     | `npm run test:coverage`         |
| Production build         | `npm run build`                 |
| Format, then review diff | `npm run format`                |
| Unused files and exports | `npm run knip`                  |

The development server needs a backend on `localhost:3000`.

ESLint and knip fail on dead code: unused props, data, emits, refs,
exports, files, and dependencies. Remove the dead code. Do not add an inline
disable or a knip ignore entry to hide it.

`npm test` runs two Vitest projects from `vite.config.js`:

- `contract`: the `*.contract.test.js` suites of pure functions, in shared
  workers without per-file isolation. They must pass in any order, and CI
  runs them shuffled: `npm run test:contract -- --sequence.shuffle`.
- `unit`: every other test file, each in a fresh worker. A suite that mocks a
  module, stubs a global, or leaves storage, timers or module state behind
  goes here.

`test/unit-setup.js` mocks `@/utils/axios.js` and `@/store.js` for every
`unit` file. A file mocks the module again for other values, or calls
`vi.unmock` for the real module. Shared HTML helpers (`textOf`, `tag`,
`tagOf`, `attribute`) are in `test/builder/fixtures.js`. `renderWithStore`
is in `test/builder/render.js`, for `unit` files only.

Test what a component does with `@vue/test-utils` in a `unit` file that
starts with `// @vitest-environment happy-dom`: mount it, use `trigger` and
`setValue`, and check focus, emitted events and the DOM. Use the server
render for static output only, and keep each file in one environment.
happy-dom has no layout, so test sizes, positions and the Vue Flow canvas in
Playwright. In `contract` suites, use `fast-check` properties for rules that
hold for every input (round trips, order, idempotence). Pass options to each
`fc.assert` call. Do not call `fc.configureGlobal`.

The build compresses Builder files with Brotli quality 9. Set
`PHENIX_BROTLI_QUALITY` (an integer from 0 to 11) to change it. The Docker
and Podman builds set 11 (see `plugins/builder-assets.js`).

## UI Conventions

- Follow two-space indentation and Prettier.
- Use existing Pinia, router, Axios, Buefy, and component patterns.
- Import Font Awesome icons individually in `src/main.js`; never import the
  entire icon set.
- UI changes must meet WCAG 2.2 AA accessibility guidelines; the Playwright
  routes smoke test enforces this with axe-core (see [e2e/README.md](e2e/README.md)).
- Authentication is selected at build time with
  `VITE_AUTH=enabled|disabled|proxy`. Validate the affected mode and keep UI,
  REST, websocket, and RBAC behavior aligned.

## Browser Tests

Playwright requires a running `phenix ui`; default URL is
`http://127.0.0.1:3000`, override with `E2E_BASE_URL`. Locally, start the
server on another port (see the port rule in the root `AGENTS.md`):

```bash
cd e2e
npm ci
npx playwright install --with-deps chromium firefox
# against `phenix ui --listen-endpoint 127.0.0.1:3080`
E2E_BASE_URL=http://127.0.0.1:3080 npx playwright test
```

`routes.spec.js` also runs an axe-core WCAG 2.x A/AA scan on every route; fix
violations in the UI (accessible names, contrast, ARIA) rather than excluding
rules. Lifecycle tests additionally need minimega, VM images, and a topology
(`E2E_LIFECYCLE=1`). Auth suites require a matching `VITE_AUTH` build and
signing key. See `e2e/README.md`; report missing prerequisites instead of
silently skipping checks.

Axe scans run only in Chromium. Do not run them in Firefox. The Firefox
project (`@cross-browser` tests) is only for browser-compatibility tests.

On CI a failed test runs once more, and a test that passes only on that retry
is flaky and fails the run (`failOnFlakyTests`). Fix the cause instead of
adding waits. A check that something does not happen runs the page clock
past the delay the UI waits for (`page.clock.runFor`), not a fixed
`waitForTimeout` (see `e2e/README.md`).

Before you change the Builder (`src/builder/`, `src/components/builder/`,
`src/views/Builder.vue`, or the `builder*` e2e specs), read
[`../../skills/phenix/references/builder.md`](../../skills/phenix/references/builder.md)
and then the reference that its routing table names.

## CI

`.github/workflows/frontend.yml` runs knip and Vitest, builds the UI and a
real backend once for each `VITE_AUTH` mode, then runs the Playwright tests
in parallel jobs, each against its own server (see `e2e/README.md`). It
builds and runs the Playwright tests only for a change that can affect the
browser, as its `changes` job decides. A change to unit tests under `test/`
alone runs Vitest only. Keep Node versions, npm cache lockfiles, auth build
mode, backend startup, path filters, and that job's path list aligned with
local commands. The Playwright jobs run in the Playwright image pinned in the
workflow. When you change the `@playwright/test` version in `e2e/`, change the
image tag and digest to match. The `changes` job fails while they differ.
