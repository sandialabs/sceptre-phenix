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
| Vitest with coverage     | `npm run test:coverage`         |
| Production build         | `npm run build`                 |
| Format, then review diff | `npm run format`                |

The development server needs a backend on `localhost:3000`.

`test/unit-setup.js` mocks `@/utils/axios.js` and `@/store.js` for every
test file. A file mocks the module again for other values, or calls
`vi.unmock` for the real module.

Test what a component does with `@vue/test-utils` in a file that starts
with `// @vitest-environment happy-dom`: mount it, use `trigger` and
`setValue`, and check focus, emitted events and the DOM. Use the server
render for static output only, and keep each file in one environment.
happy-dom has no layout, so test sizes and positions in Playwright.

## UI Conventions

- Follow two-space indentation and Prettier.
- Use existing Pinia, router, Axios, Buefy, and component patterns.
- Import Font Awesome icons individually in `src/main.js`; never import the
  entire icon set.
- UI changes must meet WCAG 2.2 AA accessibility guidelines and the axe-core AAA
  rules, including 7:1 text contrast; the Playwright smoke tests enforce this
  with axe-core (see [e2e/README.md](e2e/README.md)).
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

`routes.spec.js` also runs an axe-core WCAG 2.x A, AA, and AAA (`wcag2aaa`)
scan on every route, and `a11y-seeded.spec.js` runs it on views with data; fix
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

## CI

`.github/workflows/frontend.yml` runs Vitest, builds the UI and a
real backend once for each `VITE_AUTH` mode, then runs the Playwright tests
in parallel jobs, each against its own server (see `e2e/README.md`). It
builds and runs the Playwright tests only for a change that can affect the
browser, as its `changes` job decides. A change to unit tests under `test/`
alone runs Vitest only. Keep Node versions, npm cache lockfiles, auth build
mode, backend startup, path filters, and that job's path list aligned with
local commands. The Playwright jobs run in the Playwright image pinned in the
workflow. When you change the `@playwright/test` version in `e2e/`, change the
image tag and digest to match. The `changes` job fails while they differ.
