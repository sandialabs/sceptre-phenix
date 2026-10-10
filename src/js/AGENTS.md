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
| Production build         | `npm run build`                 |
| Format, then review diff | `npm run format`                |

The development server needs a backend on `localhost:3000`.

`npm test` runs two Vitest projects, set in `vite.config.js`: `contract`,
the suites of pure functions listed there, in shared workers without
per-file isolation, and `unit`, every other test file, each in a fresh
worker. A suite that mocks a module, stubs a global, or leaves storage,
timers or module state behind goes in `unit`. The contract suites must pass
in any order: `npx vitest run --project contract --sequence.shuffle`.

The build compresses Builder's files with Brotli quality 9;
`PHENIX_BROTLI_QUALITY` (a whole number from 0 to 11) changes it, and the
Docker and Podman builds set 11 (see `plugins/builder-assets.js`).

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

Before changing the Builder (`src/builder/`, `src/components/builder/`,
`src/views/Builder.vue`, or the `builder*` e2e specs), read
[`../../skills/phenix/references/builder.md`](../../skills/phenix/references/builder.md),
then the reference its routing table names for the task.

## CI

`.github/workflows/frontend.yml` runs Vitest, builds the UI and a real backend
once, then runs the Playwright tests in parallel jobs, each against its own
server (see `e2e/README.md`). It builds and runs the Playwright tests only
for a change that can affect the browser, as its `changes` job decides; a
change to unit tests under `test/` alone runs Vitest only. Keep Node
versions, npm cache lockfiles, auth build mode, backend startup, path
filters, and that job's path list aligned with local commands.
