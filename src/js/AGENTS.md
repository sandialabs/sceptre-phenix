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

| Purpose | Command |
|---|---|
| Development server | `npm run dev` |
| Focused Vitest | `npm test -- test/rbac.test.js` |
| All Vitest | `npm test` |
| Production build | `npm run build` |
| Format, then review diff | `npm run format` |

The development server needs a backend on `localhost:3000`.

## UI Conventions

- Follow two-space indentation and Prettier.
- Use existing Pinia, router, Axios, Buefy, and component patterns.
- Import Font Awesome icons individually in `src/main.js`; never import the
  entire icon set.
- Authentication is selected at build time with
  `VITE_AUTH=enabled|disabled|proxy`. Validate the affected mode and keep UI,
  REST, websocket, and RBAC behavior aligned.

## Browser Tests

Playwright requires a running `phenix ui`; default URL is
`http://127.0.0.1:3000`, override with `E2E_BASE_URL`:

```bash
cd e2e
npm ci
npx playwright install --with-deps chromium
npx playwright test
```

Default smoke tests need only a server. Lifecycle tests additionally need
minimega, VM images, and a topology (`E2E_LIFECYCLE=1`). Auth suites require a
matching `VITE_AUTH` build and signing key. See `e2e/README.md`; report missing
prerequisites instead of silently skipping checks.

## CI

`.github/workflows/frontend.yml` runs Vitest and builds the UI, then builds and
starts a real backend for Playwright smoke tests. Keep Node versions, npm cache
lockfiles, auth build mode, backend startup, and path filters aligned with local
commands.
