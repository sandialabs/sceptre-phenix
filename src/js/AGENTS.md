# Frontend Instructions

Read root [`AGENTS.md`](../../AGENTS.md) first. This UI uses Vue 3. These rules
apply to Vue, JavaScript/TypeScript, Vite, Vitest, and Playwright work under
`src/js/`.

## Architecture

- `src/views/`: route-level pages; `src/components/`: small reusable UI.
- `src/utils/`: shared helpers; `router.js`: routes and guards; `store.js`:
  Pinia state; `main.js`: application setup.
- Vite proxies `/api/v1`, `/version`, `/features`, and `/webshark` to
  `localhost:3000`.
- `dist/` is generated output and is copied into `src/go/web/public/`; do not
  treat it as source.

## Setup and Commands

Use Node.js 24 and locked installs:

```bash
nvm use
npm ci
```

| Purpose                  | Command                                                  |
| ------------------------ | -------------------------------------------------------- |
| Development server       | `npm run dev`                                            |
| Focused Vitest           | `npm test -- test/rbac.test.js`                          |
| All Vitest               | `npm test`                                               |
| Production build         | `npm run build`                                          |
| Format, then review diff | `npm run format`                                         |
| Bundle size budgets      | `cd perf && npm ci && npm run size` (after a build)      |
| Lighthouse audits        | `cd perf && npm run lighthouse` (needs a running server) |

The development server needs a backend on `localhost:3000`.

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
- phēnix is used on desktop browsers only, not on phones or other mobile
  devices. Do not design, test, or take screenshots for phone widths; lay pages
  out for desktop screens.

## Browser Tests

Playwright requires a running `phenix ui`; default URL is
`http://127.0.0.1:3000`, override with `E2E_BASE_URL`. Locally, start the
server on another port (see the port rule in the root `AGENTS.md`):

```bash
cd e2e
npm ci
npx playwright install --with-deps chromium
# against `phenix ui --listen-endpoint 127.0.0.1:3080`
E2E_BASE_URL=http://127.0.0.1:3080 npx playwright test
```

Default smoke tests need only a server. `routes.spec.js` also runs an
axe-core WCAG 2.x A/AA scan on every route; fix violations in the UI
(accessible names, contrast, ARIA) rather than excluding rules. Lifecycle tests additionally need
minimega, VM images, and a topology (`E2E_LIFECYCLE=1`). Auth suites require a
matching `VITE_AUTH` build and signing key. See `e2e/README.md`; report missing
prerequisites instead of silently skipping checks.

## Performance

`perf/` holds size-limit budgets and Lighthouse CI assertions; see
`perf/README.md`. Register new Buefy components in `src/utils/buefy.js` (or
locally in the one view that uses a heavy one) rather than installing all of
Buefy, keep long-lived third-party objects (xterm, d3, editors) out of deep
reactivity with `markRaw`, and clean up intervals, sockets, and listeners in
`beforeUnmount`.

## CI

`.github/workflows/frontend.yml` runs Vitest and builds the UI, then builds and
starts a real backend for Playwright smoke tests. Its `perf` job checks bundle
budgets and runs Lighthouse CI against a real backend. Keep Node versions, npm
cache lockfiles, auth build mode, backend startup, and path filters aligned with
local commands.
